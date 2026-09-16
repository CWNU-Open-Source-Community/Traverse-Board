//go:build windows && desktop && wv2runtime.error

package main

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"encoding/xml"
	"os"
	"path/filepath"
	"testing"
)

// Check both checked-in architectures and the linked executable. The normal
// desktop build runs this test, so stale icon-only .syso files cannot silently
// remove DPI awareness when assets are regenerated or the link path changes.
func TestDesktopWindowsResourcesPreserveDPIAndIcons(t *testing.T) {
	manifest, err := os.ReadFile(filepath.Join("..", "..", "packaging", "windows", "TraverseBoard.exe.manifest"))
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Application struct {
			WindowsSettings struct {
				DPIAware     string `xml:"http://schemas.microsoft.com/SMI/2005/WindowsSettings dpiAware"`
				DPIAwareness string `xml:"http://schemas.microsoft.com/SMI/2016/WindowsSettings dpiAwareness"`
			} `xml:"windowsSettings"`
		} `xml:"urn:schemas-microsoft-com:asm.v3 application"`
		TrustInfo struct {
			Security struct {
				Privileges struct {
					Level struct {
						Level    string `xml:"level,attr"`
						UIAccess string `xml:"uiAccess,attr"`
					} `xml:"requestedExecutionLevel"`
				} `xml:"requestedPrivileges"`
			} `xml:"security"`
		} `xml:"urn:schemas-microsoft-com:asm.v3 trustInfo"`
		Compatibility struct {
			Application struct {
				SupportedOS struct {
					ID string `xml:"Id,attr"`
				} `xml:"supportedOS"`
			} `xml:"application"`
		} `xml:"urn:schemas-microsoft-com:compatibility.v1 compatibility"`
	}
	if err := xml.Unmarshal(manifest, &settings); err != nil {
		t.Fatal(err)
	}
	if settings.Application.WindowsSettings.DPIAware != "true/pm" ||
		settings.Application.WindowsSettings.DPIAwareness != "permonitorv2,permonitor" {
		t.Fatal("desktop manifest must retain Wails per-monitor v2 DPI awareness and fallback")
	}
	if level := settings.TrustInfo.Security.Privileges.Level; level.Level != "asInvoker" || level.UIAccess != "false" {
		t.Fatal("desktop manifest must not request elevation or UI access")
	}
	if settings.Compatibility.Application.SupportedOS.ID != "{8e0f7a12-bfb3-4fe8-b9a5-48fd50a15a9a}" {
		t.Fatal("desktop manifest must declare its Windows 10/11 compatibility")
	}
	icon, err := os.ReadFile(filepath.Join("..", "..", "packaging", "windows", "TraverseBoard.ico"))
	if err != nil {
		t.Fatal(err)
	}
	count := int(binary.LittleEndian.Uint16(icon[4:6]))
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"traverse_board_windows_amd64.syso", "traverse_board_windows_arm64.syso", executable} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			resources := desktopResourcePayloads(t, path)
			if values := resources[24]; len(values) != 1 || !bytes.Equal(values[0], manifest) {
				t.Fatal("RT_MANIFEST must embed the exact shared manifest")
			}
			if len(resources[14]) != 1 || len(resources[3]) != count {
				t.Fatalf("approved icon group/images missing: groups=%d images=%d want=%d", len(resources[14]), len(resources[3]), count)
			}
			for i := range count {
				entry := icon[6+i*16 : 6+(i+1)*16]
				size, offset := binary.LittleEndian.Uint32(entry[8:12]), binary.LittleEndian.Uint32(entry[12:16])
				if !bytes.Equal(resources[3][i], icon[offset:offset+size]) {
					t.Fatalf("embedded icon image %d differs from the approved ICO", i)
				}
			}
		})
	}
}

// The three-level resource tree has the same layout in the COFF input and PE
// output; only leaf data addresses change from section offsets to image RVAs.
func desktopResourcePayloads(t *testing.T, path string) map[uint32][][]byte {
	t.Helper()
	file, err := pe.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	section := file.Section(".rsrc")
	if section == nil {
		t.Fatal("desktop has no Windows resources")
	}
	data, err := section.Data()
	if err != nil {
		t.Fatal(err)
	}
	span := func(offset, size uint32) []byte {
		if uint64(offset)+uint64(size) > uint64(len(data)) {
			t.Fatalf("resource data outside section: %d+%d", offset, size)
		}
		return data[offset : offset+size]
	}
	result := make(map[uint32][][]byte)
	var walk func(uint32, int, uint32)
	walk = func(offset uint32, depth int, kind uint32) {
		if depth > 2 {
			t.Fatal("unexpected resource tree depth")
		}
		header := span(offset, 16)
		count := uint32(binary.LittleEndian.Uint16(header[12:14])) + uint32(binary.LittleEndian.Uint16(header[14:16]))
		for i := range count {
			entry := span(offset+16+i*8, 8)
			id, target := binary.LittleEndian.Uint32(entry[:4]), binary.LittleEndian.Uint32(entry[4:])
			resourceType := kind
			if depth == 0 {
				resourceType = id
			}
			if target&0x80000000 != 0 {
				walk(target&0x7fffffff, depth+1, resourceType)
				continue
			}
			if depth != 2 {
				t.Fatal("resource leaf has unexpected depth")
			}
			leaf := span(target, 16)
			rva, size := binary.LittleEndian.Uint32(leaf[:4]), binary.LittleEndian.Uint32(leaf[4:8])
			if rva < section.VirtualAddress {
				t.Fatal("resource RVA precedes section")
			}
			result[resourceType] = append(result[resourceType], span(rva-section.VirtualAddress, size))
		}
	}
	walk(0, 0, 0)
	return result
}
