//go:build windows

package browserruntime

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	fwpmSessionFlagDynamic         = 0x00000001
	fwpmFilterFlagClearActionRight = 0x00000008
	fwpEmpty                       = 0
	fwpUint8                       = 1
	fwpUint16                      = 2
	fwpUint32                      = 3
	fwpByteArray16Type             = 11
	fwpByteBlobType                = 12
	fwpMatchEqual                  = 0
	fwpActionFlagTerminating       = 0x00001000
	fwpActionBlock                 = 0x00000001 | fwpActionFlagTerminating
	fwpActionPermit                = 0x00000002 | fwpActionFlagTerminating
	rpcAuthnWinNT                  = 10
	ipProtocolTCP                  = 6
	wfpSubLayerWeight              = 0xf000
	fwpEFilterNotFound             = 0x80320003
)

var (
	fwpuclntDLL                = windows.NewLazySystemDLL("fwpuclnt.dll")
	procFwpmEngineOpen0        = fwpuclntDLL.NewProc("FwpmEngineOpen0")
	procFwpmEngineClose0       = fwpuclntDLL.NewProc("FwpmEngineClose0")
	procFwpmTransactionBegin0  = fwpuclntDLL.NewProc("FwpmTransactionBegin0")
	procFwpmTransactionCommit0 = fwpuclntDLL.NewProc("FwpmTransactionCommit0")
	procFwpmTransactionAbort0  = fwpuclntDLL.NewProc("FwpmTransactionAbort0")
	procFwpmSubLayerAdd0       = fwpuclntDLL.NewProc("FwpmSubLayerAdd0")
	procFwpmFilterAdd0         = fwpuclntDLL.NewProc("FwpmFilterAdd0")
	procFwpmFilterGetByID0     = fwpuclntDLL.NewProc("FwpmFilterGetById0")
	procFwpmGetAppIDFromFile   = fwpuclntDLL.NewProc("FwpmGetAppIdFromFileName0")
	procFwpmFreeMemory0        = fwpuclntDLL.NewProc("FwpmFreeMemory0")

	fwpmLayerALEAuthConnectV4  = mustWindowsGUID("c38d57d1-05a7-4c33-904f-7fbceee60e82")
	fwpmLayerALEAuthConnectV6  = mustWindowsGUID("4a72393b-319f-44bc-84c3-ba54dcb3b6b4")
	fwpmConditionALEAppID      = mustWindowsGUID("d78e1e87-8644-4ea5-9437-d809ecefc971")
	fwpmConditionRemoteAddress = mustWindowsGUID("b235ae9a-1d64-49b8-a44c-5ff3d9095045")
	fwpmConditionRemotePort    = mustWindowsGUID("c35a604d-d22b-4e1a-91b4-68f674ee674b")
	fwpmConditionIPProtocol    = mustWindowsGUID("3971ef2b-623e-4f9a-8cb1-6e79b806b9a7")
)

type fwpByteBlob struct {
	Size uint32
	_    uint32
	Data *byte
}

type fwpValue0 struct {
	Type  uint32
	_     uint32
	Value uintptr
}

type fwpConditionValue0 struct {
	Type  uint32
	_     uint32
	Value uintptr
}

type fwpmDisplayData0 struct {
	Name        *uint16
	Description *uint16
}

type fwpmSession0 struct {
	SessionKey       windows.GUID
	DisplayData      fwpmDisplayData0
	Flags            uint32
	TxnWaitTimeoutMS uint32
	ProcessID        uint32
	_                uint32
	SID              *windows.SID
	Username         *uint16
	KernelMode       int32
	_                uint32
}

type fwpmSubLayer0 struct {
	SubLayerKey  windows.GUID
	DisplayData  fwpmDisplayData0
	Flags        uint32
	_            uint32
	ProviderKey  *windows.GUID
	ProviderData fwpByteBlob
	Weight       uint16
	_            [6]byte
}

type fwpmFilterCondition0 struct {
	FieldKey       windows.GUID
	MatchType      uint32
	_              uint32
	ConditionValue fwpConditionValue0
}

type fwpmAction0 struct {
	Type uint32
	Data [16]byte
}

type fwpmFilter0 struct {
	FilterKey           windows.GUID
	DisplayData         fwpmDisplayData0
	Flags               uint32
	_                   uint32
	ProviderKey         *windows.GUID
	ProviderData        fwpByteBlob
	LayerKey            windows.GUID
	SubLayerKey         windows.GUID
	Weight              fwpValue0
	NumFilterConditions uint32
	_                   uint32
	FilterCondition     *fwpmFilterCondition0
	Action              fwpmAction0
	_                   uint32
	Context             [16]byte
	Reserved            *windows.GUID
	FilterID            uint64
	EffectiveWeight     fwpValue0
}

type windowsWFPBrowserContainmentFactory struct{}

type windowsWFPBrowserContainmentGuard struct {
	mu          sync.Mutex
	engine      windows.Handle
	filterIDs   []uint64
	fingerprint string
	closed      bool
	cleanupOK   bool
	closeErr    error
}

type windowsWFPRemoteTarget struct {
	Address netip.Addr
	Port    uint16
}

func newPlatformBrowserNetworkContainmentFactory() browserNetworkContainmentFactory {
	return windowsWFPBrowserContainmentFactory{}
}

func (windowsWFPBrowserContainmentFactory) Name() string {
	return WindowsWFPBrowserContainmentAdapterName
}

func (windowsWFPBrowserContainmentFactory) Available() bool {
	return windows.GetCurrentProcessToken().IsElevated() && fwpuclntDLL.Load() == nil &&
		procFwpmEngineOpen0.Find() == nil && procFwpmFilterAdd0.Find() == nil
}

func (windowsWFPBrowserContainmentFactory) Prepare(
	plan BrowserNetworkContainmentPlan,
) (browserNetworkContainmentGuard, error) {
	if err := validateBrowserNetworkContainmentPlanStructure(plan); err != nil {
		return nil, err
	}
	if !windows.GetCurrentProcessToken().IsElevated() {
		return nil, fmt.Errorf("%w: Windows WFP containment requires an elevated helper", ErrBrowserRuntimeUnavailable)
	}
	if err := rejectExistingExecutableProcesses(plan.ExecutablePath); err != nil {
		return nil, err
	}
	address, err := netip.ParseAddr(plan.TargetAddress)
	if err != nil {
		return nil, ErrBrowserRuntimeBoundary
	}
	engine, filterIDs, err := installWindowsWFPBrowserFiltersForTargets(
		plan.ExecutablePath,
		[]windowsWFPRemoteTarget{{Address: address, Port: plan.TargetPort}},
		"Prayu restricted browser")
	if err != nil {
		return nil, err
	}
	guard := &windowsWFPBrowserContainmentGuard{
		engine: engine, filterIDs: append([]uint64(nil), filterIDs...),
	}
	guard.fingerprint = browserRuntimeFingerprint(struct {
		Adapter         string   `json:"adapter"`
		PlanFingerprint string   `json:"plan_fingerprint"`
		FilterIDs       []uint64 `json:"filter_ids"`
	}{WindowsWFPBrowserContainmentAdapterName, plan.Fingerprint, guard.filterIDs})
	return guard, nil
}

func (guard *windowsWFPBrowserContainmentGuard) Adapter() string {
	return WindowsWFPBrowserContainmentAdapterName
}

func (guard *windowsWFPBrowserContainmentGuard) Fingerprint() string {
	if guard == nil {
		return ""
	}
	return guard.fingerprint
}

func (guard *windowsWFPBrowserContainmentGuard) Close() error {
	if guard == nil {
		return nil
	}
	guard.mu.Lock()
	defer guard.mu.Unlock()
	if guard.closed {
		return guard.closeErr
	}
	guard.closed = true
	if guard.engine == 0 {
		guard.closeErr = errors.New("WFP containment guard lost its engine handle")
		return guard.closeErr
	}
	result, _, _ := procFwpmEngineClose0.Call(uintptr(guard.engine))
	guard.engine = 0
	if result != 0 {
		guard.closeErr = syscall.Errno(result)
		return guard.closeErr
	}
	guard.closeErr = verifyWindowsWFPFiltersRemoved(guard.filterIDs)
	guard.cleanupOK = guard.closeErr == nil
	return guard.closeErr
}

func (guard *windowsWFPBrowserContainmentGuard) CleanupVerified() bool {
	if guard == nil {
		return false
	}
	guard.mu.Lock()
	defer guard.mu.Unlock()
	return guard.closed && guard.cleanupOK
}

func installWindowsWFPBrowserFiltersForTargets(executablePath string,
	targets []windowsWFPRemoteTarget, label string,
) (
	windows.Handle, []uint64, error,
) {
	if !filepath.IsAbs(executablePath) || len(targets) == 0 || strings.TrimSpace(label) == "" {
		return 0, nil, ErrBrowserRuntimeBoundary
	}
	for _, target := range targets {
		if !target.Address.IsValid() || target.Address.IsUnspecified() || target.Port == 0 {
			return 0, nil, ErrBrowserRuntimeBoundary
		}
	}
	name, _ := windows.UTF16PtrFromString(label)
	description, _ := windows.UTF16PtrFromString("Dynamic exact-target browser network containment")
	sessionKey, err := windows.GenerateGUID()
	if err != nil {
		return 0, nil, err
	}
	session := fwpmSession0{
		SessionKey: sessionKey, DisplayData: fwpmDisplayData0{Name: name, Description: description},
		Flags: fwpmSessionFlagDynamic, TxnWaitTimeoutMS: 5_000,
		ProcessID: uint32(os.Getpid()),
	}
	var engine windows.Handle
	if err := wfpCall(procFwpmEngineOpen0, 0, rpcAuthnWinNT, 0,
		uintptr(unsafe.Pointer(&session)), uintptr(unsafe.Pointer(&engine))); err != nil {
		return 0, nil, fmt.Errorf("open dynamic WFP engine: %w", err)
	}
	closeEngine := true
	defer func() {
		if closeEngine {
			_, _, _ = procFwpmEngineClose0.Call(uintptr(engine))
		}
	}()

	executable, err := windows.UTF16PtrFromString(executablePath)
	if err != nil {
		return 0, nil, ErrBrowserRuntimeBoundary
	}
	var appID *fwpByteBlob
	if err := wfpCall(procFwpmGetAppIDFromFile, uintptr(unsafe.Pointer(executable)),
		uintptr(unsafe.Pointer(&appID))); err != nil {
		return 0, nil, fmt.Errorf("derive WFP application id: %w", err)
	}
	if appID == nil || appID.Size == 0 || appID.Data == nil {
		return 0, nil, ErrBrowserRuntimeBoundary
	}
	defer func() {
		pointer := unsafe.Pointer(appID)
		_, _, _ = procFwpmFreeMemory0.Call(uintptr(unsafe.Pointer(&pointer)))
	}()

	if err := wfpCall(procFwpmTransactionBegin0, uintptr(engine), 0); err != nil {
		return 0, nil, fmt.Errorf("begin WFP containment transaction: %w", err)
	}
	transactionActive := true
	defer func() {
		if transactionActive {
			_, _, _ = procFwpmTransactionAbort0.Call(uintptr(engine))
		}
	}()

	subLayerKey, err := windows.GenerateGUID()
	if err != nil {
		return 0, nil, err
	}
	subLayer := fwpmSubLayer0{
		SubLayerKey: subLayerKey,
		DisplayData: fwpmDisplayData0{Name: name, Description: description},
		Weight:      wfpSubLayerWeight,
	}
	if err := wfpCall(procFwpmSubLayerAdd0, uintptr(engine),
		uintptr(unsafe.Pointer(&subLayer)), 0); err != nil {
		return 0, nil, fmt.Errorf("add dynamic WFP sublayer: %w", err)
	}

	appCondition := []fwpmFilterCondition0{
		wfpPointerCondition(fwpmConditionALEAppID, fwpByteBlobType, unsafe.Pointer(appID)),
	}
	filterIDs := make([]uint64, 0, len(targets)+2)
	for index, target := range targets {
		allowID, err := addWindowsWFPRemotePermit(engine, subLayerKey, appID,
			target, fmt.Sprintf("Prayu controlled target permit %d", index+1))
		if err != nil {
			return 0, nil, err
		}
		filterIDs = append(filterIDs, allowID)
	}
	blockV4, err := addWindowsWFPFilter(engine, subLayerKey, fwpmLayerALEAuthConnectV4,
		"Prayu IPv4 default deny", fwpActionBlock, 14,
		fwpmFilterFlagClearActionRight, appCondition)
	if err != nil {
		return 0, nil, err
	}
	filterIDs = append(filterIDs, blockV4)
	blockV6, err := addWindowsWFPFilter(engine, subLayerKey, fwpmLayerALEAuthConnectV6,
		"Prayu IPv6 default deny", fwpActionBlock, 14,
		fwpmFilterFlagClearActionRight, appCondition)
	if err != nil {
		return 0, nil, err
	}
	filterIDs = append(filterIDs, blockV6)
	if err := wfpCall(procFwpmTransactionCommit0, uintptr(engine)); err != nil {
		return 0, nil, fmt.Errorf("commit WFP containment transaction: %w", err)
	}
	transactionActive = false
	closeEngine = false
	return engine, filterIDs, nil
}

func addWindowsWFPRemotePermit(engine windows.Handle, subLayerKey windows.GUID,
	appID *fwpByteBlob, target windowsWFPRemoteTarget, label string,
) (uint64, error) {
	conditions := []fwpmFilterCondition0{
		wfpPointerCondition(fwpmConditionALEAppID, fwpByteBlobType, unsafe.Pointer(appID)),
		wfpScalarCondition(fwpmConditionRemotePort, fwpUint16, uintptr(target.Port)),
		wfpScalarCondition(fwpmConditionIPProtocol, fwpUint8, ipProtocolTCP),
	}
	layer := fwpmLayerALEAuthConnectV4
	if target.Address.Is4() {
		address, err := netIPv4HostOrder(target.Address.String())
		if err != nil {
			return 0, err
		}
		conditions = append(conditions,
			wfpScalarCondition(fwpmConditionRemoteAddress, fwpUint32, uintptr(address)))
	} else if target.Address.Is6() {
		layer = fwpmLayerALEAuthConnectV6
		address := target.Address.As16()
		conditions = append(conditions, wfpPointerCondition(
			fwpmConditionRemoteAddress, fwpByteArray16Type, unsafe.Pointer(&address[0])))
	} else {
		return 0, ErrBrowserRuntimeBoundary
	}
	return addWindowsWFPFilter(engine, subLayerKey, layer, label,
		fwpActionPermit, 15, 0, conditions)
}

func verifyWindowsWFPFiltersRemoved(filterIDs []uint64) error {
	if len(filterIDs) == 0 {
		return ErrBrowserRuntimeBoundary
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		remaining, err := windowsWFPFiltersRemaining(filterIDs)
		if err != nil {
			return err
		}
		if len(remaining) == 0 {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("dynamic WFP filters remained after engine close: %v", remaining)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func windowsWFPFiltersRemaining(filterIDs []uint64) ([]uint64, error) {
	var engine windows.Handle
	if err := wfpCall(procFwpmEngineOpen0, 0, rpcAuthnWinNT, 0, 0,
		uintptr(unsafe.Pointer(&engine))); err != nil {
		return nil, fmt.Errorf("open WFP engine for cleanup verification: %w", err)
	}
	defer func() { _, _, _ = procFwpmEngineClose0.Call(uintptr(engine)) }()
	remaining := make([]uint64, 0)
	for _, filterID := range filterIDs {
		var filter *fwpmFilter0
		result, _, _ := procFwpmFilterGetByID0.Call(uintptr(engine), uintptr(filterID),
			uintptr(unsafe.Pointer(&filter)))
		switch uint32(result) {
		case 0:
			remaining = append(remaining, filterID)
			if filter != nil {
				pointer := unsafe.Pointer(filter)
				_, _, _ = procFwpmFreeMemory0.Call(uintptr(unsafe.Pointer(&pointer)))
			}
		case fwpEFilterNotFound:
		default:
			return nil, fmt.Errorf("query WFP filter %d: %w", filterID,
				syscall.Errno(uint32(result)))
		}
	}
	return remaining, nil
}

func addWindowsWFPFilter(engine windows.Handle, subLayerKey windows.GUID,
	layer windows.GUID, label string, action uint32, weight byte, flags uint32,
	conditions []fwpmFilterCondition0,
) (uint64, error) {
	name, err := windows.UTF16PtrFromString(label)
	if err != nil {
		return 0, err
	}
	filter := fwpmFilter0{
		DisplayData: fwpmDisplayData0{Name: name}, Flags: flags,
		LayerKey: layer, SubLayerKey: subLayerKey,
		Weight:              fwpValue0{Type: fwpUint8, Value: uintptr(weight)},
		NumFilterConditions: uint32(len(conditions)),
		Action:              fwpmAction0{Type: action},
	}
	if len(conditions) > 0 {
		filter.FilterCondition = &conditions[0]
	}
	var id uint64
	if err := wfpCall(procFwpmFilterAdd0, uintptr(engine),
		uintptr(unsafe.Pointer(&filter)), 0, uintptr(unsafe.Pointer(&id))); err != nil {
		return 0, fmt.Errorf("add WFP filter %q: %w", label, err)
	}
	if id == 0 {
		return 0, errors.New("WFP returned an empty filter id")
	}
	return id, nil
}

func wfpPointerCondition(key windows.GUID, dataType uint32,
	pointer unsafe.Pointer,
) fwpmFilterCondition0 {
	return fwpmFilterCondition0{
		FieldKey: key, MatchType: fwpMatchEqual,
		ConditionValue: fwpConditionValue0{Type: dataType, Value: uintptr(pointer)},
	}
}

func wfpScalarCondition(key windows.GUID, dataType uint32,
	value uintptr,
) fwpmFilterCondition0 {
	return fwpmFilterCondition0{
		FieldKey: key, MatchType: fwpMatchEqual,
		ConditionValue: fwpConditionValue0{Type: dataType, Value: value},
	}
}

func wfpCall(procedure *windows.LazyProc, arguments ...uintptr) error {
	if err := procedure.Find(); err != nil {
		return err
	}
	result, _, _ := procedure.Call(arguments...)
	if result != 0 {
		return syscall.Errno(result)
	}
	return nil
}

func netIPv4HostOrder(raw string) (uint32, error) {
	address, err := netip.ParseAddr(raw)
	if err != nil || !address.Is4() {
		return 0, ErrBrowserRuntimeBoundary
	}
	bytes := address.As4()
	return uint32(bytes[0])<<24 | uint32(bytes[1])<<16 |
		uint32(bytes[2])<<8 | uint32(bytes[3]), nil
}

func rejectExistingExecutableProcesses(executablePath string) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return fmt.Errorf("snapshot browser processes: %w", err)
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return fmt.Errorf("enumerate browser processes: %w", err)
	}
	expectedBase := filepath.Base(executablePath)
	for {
		base := windows.UTF16ToString(entry.ExeFile[:])
		if strings.EqualFold(base, expectedBase) {
			path, queryErr := processImagePath(entry.ProcessID)
			if queryErr != nil {
				return fmt.Errorf("cannot exclude an existing %s process: %w", expectedBase, queryErr)
			}
			if strings.EqualFold(filepath.Clean(path), filepath.Clean(executablePath)) {
				return errors.New("accepted browser executable is already running; WFP path rules would affect that process")
			}
		}
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				break
			}
			return fmt.Errorf("enumerate browser processes: %w", err)
		}
	}
	return nil
}

func processImagePath(processID uint32) (string, error) {
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION,
		false, processID)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(process)
	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(process, 0, &buffer[0], &size); err != nil {
		return "", err
	}
	if size == 0 || int(size) > len(buffer) {
		return "", ErrBrowserRuntimeBoundary
	}
	return windows.UTF16ToString(buffer[:size]), nil
}

func validateBrowserNetworkContainmentPlanStructure(plan BrowserNetworkContainmentPlan) error {
	address, err := netip.ParseAddr(plan.TargetAddress)
	if err != nil || !address.Is4() || !address.IsLoopback() ||
		plan.ProtocolVersion != BrowserNetworkContainmentPlanProtocolVersion ||
		!validSHA256(plan.SessionPlanFingerprint) ||
		!validSHA256(plan.ExecutableIdentityFingerprint) ||
		!validSHA256(plan.EvidenceFingerprint) || !validSHA256(plan.ReviewFingerprint) ||
		!filepath.IsAbs(plan.ExecutablePath) || plan.TargetPort == 0 ||
		plan.TransportProtocol != "tcp" ||
		plan.Adapter != WindowsWFPBrowserContainmentAdapterName ||
		plan.PolicyVersion != BrowserNetworkContainmentPolicyVersion ||
		!plan.DynamicSessionRequired || !plan.AtomicInstallRequired ||
		!plan.DefaultDenyIPv4 || !plan.DefaultDenyIPv6 || !plan.ExactTargetOnly ||
		plan.DNSAuthorized || plan.ProxyAuthorized || plan.ExistingProcessAllowed ||
		plan.CDPUsedAsEvidence || !plan.NetworkAuthorized || plan.CreatedAt.IsZero() ||
		!plan.ExpiresAt.After(plan.CreatedAt) ||
		plan.Fingerprint != browserRuntimeFingerprint(plan) {
		return ErrBrowserRuntimeBoundary
	}
	return nil
}

func mustWindowsGUID(value string) windows.GUID {
	guid, err := windows.GUIDFromString("{" + strings.Trim(value, "{}") + "}")
	if err != nil {
		panic(err)
	}
	return guid
}
