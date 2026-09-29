package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
)

const SessionSteeringPromotionPathTemplate = "/api/v1/sessions/{session_id}/messages/{message_id}/promote"
const SessionSteeringPromotionObservationPathTemplate = "/api/v1/sessions/{session_id}/messages/{message_id}/promotions/{operation_key}"

type SessionSteeringPromotionRequestView struct {
	Version               string `json:"version"`
	ExpectedRevision      *int64 `json:"expected_revision"`
	ExpectedContentSHA256 string `json:"expected_content_sha256"`
	ExpectedAttemptID     string `json:"expected_attempt_id"`
	ExpectedExecutionID   string `json:"expected_execution_id"`
}
type SessionSteeringPromotionReceiptView struct {
	ID                   string    `json:"id"`
	ReplacementMessageID string    `json:"replacement_message_id"`
	ExpectedRevision     int64     `json:"expected_revision"`
	ContentSHA256        string    `json:"content_sha256"`
	TargetAttemptID      string    `json:"target_attempt_id"`
	ExecutionID          string    `json:"execution_id"`
	CancellationID       string    `json:"cancellation_id"`
	CreatedAt            time.Time `json:"created_at"`
}
type SessionSteeringPromotionView struct {
	Version          string                              `json:"version"`
	RunID            string                              `json:"run_id"`
	SessionID        string                              `json:"session_id"`
	MessageID        string                              `json:"message_id"`
	Receipt          SessionSteeringPromotionReceiptView `json:"receipt"`
	Replayed         bool                                `json:"replayed"`
	ExecutionStarted bool                                `json:"execution_started"`
	ModelCalled      bool                                `json:"model_called"`
	ToolCalled       bool                                `json:"tool_called"`
	CapabilityGrant  bool                                `json:"capability_grant"`
}
type SessionSteeringPromotionRejectionReceiptView struct {
	ID               string    `json:"id"`
	ExpectedRevision int64     `json:"expected_revision"`
	ContentSHA256    string    `json:"content_sha256"`
	TargetAttemptID  string    `json:"target_attempt_id"`
	ExecutionID      string    `json:"execution_id"`
	CreatedAt        time.Time `json:"created_at"`
}
type SessionSteeringPromotionRejectionView struct {
	Version          string                                       `json:"version"`
	RunID            string                                       `json:"run_id"`
	SessionID        string                                       `json:"session_id"`
	MessageID        string                                       `json:"message_id"`
	Receipt          SessionSteeringPromotionRejectionReceiptView `json:"receipt"`
	Rejected         bool                                         `json:"rejected"`
	Replayed         bool                                         `json:"replayed"`
	ExecutionStarted bool                                         `json:"execution_started"`
	ModelCalled      bool                                         `json:"model_called"`
	ToolCalled       bool                                         `json:"tool_called"`
	CapabilityGrant  bool                                         `json:"capability_grant"`
}
type SessionSteeringPromotionObservationView struct {
	Version           string                                  `json:"version"`
	SessionID         string                                  `json:"session_id"`
	MessageID         string                                  `json:"message_id"`
	State             string                                  `json:"state"`
	Promotion         *SessionSteeringPromotionView           `json:"promotion,omitempty"`
	Rejection         *SessionSteeringPromotionRejectionView  `json:"rejection,omitempty"`
	Message           *OperatorSteeringObservationMessageView `json:"message,omitempty"`
	CapabilityGrant   bool                                    `json:"capability_grant"`
	ExecutionObserved bool                                    `json:"execution_observed"`
	ExecutionID       string                                  `json:"execution_id,omitempty"`
}

func matchSessionSteeringPromotionPath(path string) (string, string, bool) {
	parts := strings.Split(strings.TrimPrefix(path, "/api/v1/sessions/"), "/")
	if !strings.HasPrefix(path, "/api/v1/sessions/") || len(parts) != 4 || parts[1] != "messages" || parts[3] != "promote" {
		return "", "", false
	}
	return parts[0], parts[2], true
}
func promotionView(r domain.OperatorSteeringPromotionReceipt, replayed bool) SessionSteeringPromotionView {
	return SessionSteeringPromotionView{Version: domain.SessionSteeringPromotionProtocolVersion, RunID: r.RunID,
		SessionID: r.SessionID, MessageID: r.MessageID, Replayed: replayed,
		Receipt: SessionSteeringPromotionReceiptView{ID: r.ID, ReplacementMessageID: r.ReplacementMessageID,
			ExpectedRevision: r.ExpectedRevision, ContentSHA256: r.ContentSHA256, TargetAttemptID: r.TargetAttemptID,
			ExecutionID: r.ExecutionID, CancellationID: r.CancellationID, CreatedAt: r.CreatedAt}}
}
func promotionRejectionView(r domain.OperatorSteeringPromotionRejection, replayed bool) SessionSteeringPromotionRejectionView {
	return SessionSteeringPromotionRejectionView{Version: domain.SessionSteeringPromotionProtocolVersion, RunID: r.RunID, SessionID: r.SessionID, MessageID: r.MessageID,
		Rejected: true, Replayed: replayed, Receipt: SessionSteeringPromotionRejectionReceiptView{ID: r.ID, ExpectedRevision: r.ExpectedRevision, ContentSHA256: r.ContentSHA256, TargetAttemptID: r.TargetAttemptID, ExecutionID: r.ExecutionID, CreatedAt: r.CreatedAt}}
}
func (a *API) serveSessionSteeringPromotion(w http.ResponseWriter, request *http.Request, requestID, sessionID, messageID string) {
	if !a.sessionSteeringControlEnabled {
		a.writeError(w, requestID, apperror.New(apperror.CodeNotFound, "HTTP API endpoint was not found"), http.StatusNotFound)
		return
	}
	if !a.authorized(request, a.controlTokenHash) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="CyberAgent Control API"`)
		a.writeError(w, requestID, apperror.New(apperror.CodePolicyDenied, "valid control bearer authorization is required"), http.StatusUnauthorized)
		return
	}
	if request.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		a.writeError(w, requestID, apperror.New(apperror.CodeInvalidArgument, "promotion only supports POST"), http.StatusMethodNotAllowed)
		return
	}
	for _, id := range []string{sessionID, messageID} {
		if err := validatePathIdentity(id); err != nil {
			a.writeError(w, requestID, err, 0)
			return
		}
	}
	if err := rejectQuery(request.URL.Query()); err != nil {
		a.writeError(w, requestID, err, 0)
		return
	}
	if err := validateJSONContentType(request.Header); err != nil {
		a.writeError(w, requestID, err, http.StatusUnsupportedMediaType)
		return
	}
	key, err := sessionControlIdempotencyKey(request.Header, "Session steering promotion")
	if err != nil {
		a.writeError(w, requestID, err, 0)
		return
	}
	body, err := readBoundedRequestBody(request, 4*1024)
	if err != nil {
		a.writeError(w, requestID, err, http.StatusRequestEntityTooLarge)
		return
	}
	if !utf8.Valid(body) {
		a.writeError(w, requestID, apperror.New(apperror.CodeInvalidArgument, "promotion body must be UTF-8"), 0)
		return
	}
	if err := rejectDuplicateJSONObjectFields(body, "Session steering promotion"); err != nil {
		a.writeError(w, requestID, err, 0)
		return
	}
	var view SessionSteeringPromotionRequestView
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&view); err != nil {
		a.writeError(w, requestID, apperror.New(apperror.CodeInvalidArgument, "promotion body must be one JSON object"), 0)
		return
	}
	if err := ensureJSONEOF(decoder); err != nil {
		a.writeError(w, requestID, err, 0)
		return
	}
	if view.Version != domain.SessionSteeringPromotionProtocolVersion || view.ExpectedRevision == nil {
		a.writeError(w, requestID, apperror.New(apperror.CodeInvalidArgument, "promotion version and expected_revision are required"), 0)
		return
	}
	input, err := (domain.PromoteOperatorSteeringRequest{SessionID: sessionID, MessageID: messageID, ExpectedRevision: *view.ExpectedRevision,
		ExpectedContentSHA256: view.ExpectedContentSHA256, ExpectedAttemptID: view.ExpectedAttemptID, ExpectedExecutionID: view.ExpectedExecutionID,
		OperationKey: key, RequestedBy: "http_session_operator"}).Normalize()
	if err != nil {
		a.writeError(w, requestID, apperror.Wrap(apperror.CodeInvalidArgument, err.Error(), err), 0)
		return
	}
	controller, ok := a.threadTurnController.(interface {
		PromoteCurrentSteering(context.Context, domain.PromoteOperatorSteeringRequest) (domain.PromoteOperatorSteeringResult, error)
	})
	if !ok {
		a.writeError(w, requestID, apperror.New(apperror.CodeFailedPrecondition, "live task promotion control is unavailable"), 0)
		return
	}
	result, err := controller.PromoteCurrentSteering(request.Context(), input)
	if err != nil {
		a.writeError(w, requestID, err, 0)
		return
	}
	if r := result.Rejection; r != nil {
		if r.Validate() != nil || result.Receipt.ID != "" || r.SessionID != sessionID || r.MessageID != messageID || r.RequestedBy != input.RequestedBy || r.ExpectedRevision != input.ExpectedRevision || r.ContentSHA256 != input.ExpectedContentSHA256 || r.TargetAttemptID != input.ExpectedAttemptID || r.ExecutionID != input.ExpectedExecutionID {
			a.writeError(w, requestID, apperror.New(apperror.CodeConflict, "promotion rejection binding differs"), 0)
			return
		}
		a.writeSuccessStatus(w, requestID, promotionRejectionView(*r, result.Replayed), nil, http.StatusAccepted)
		return
	}
	r := result.Receipt
	if r.Validate() != nil || r.SessionID != sessionID || r.MessageID != messageID || r.RequestedBy != input.RequestedBy ||
		r.ExpectedRevision != input.ExpectedRevision || r.ContentSHA256 != input.ExpectedContentSHA256 || r.TargetAttemptID != input.ExpectedAttemptID || r.ExecutionID != input.ExpectedExecutionID {
		a.writeError(w, requestID, apperror.New(apperror.CodeConflict, "promotion receipt binding differs"), 0)
		return
	}
	a.writeSuccessStatus(w, requestID, promotionView(r, result.Replayed), nil, http.StatusAccepted)
}

func (a *API) sessionSteeringPromotionObservation(request *http.Request, sessionID, messageID, key string) (any, *Page, error) {
	if err := rejectQuery(request.URL.Query()); err != nil {
		return nil, nil, err
	}
	reader, ok := a.store.(interface {
		InspectOperatorSteeringPromotion(context.Context, string, string, string, string) (domain.OperatorSteeringPromotionInspection, error)
	})
	if !ok {
		return nil, nil, apperror.New(apperror.CodeFailedPrecondition, "promotion observation is unavailable")
	}
	inspect := reader.InspectOperatorSteeringPromotion
	if controller, ok := a.threadTurnController.(interface {
		InspectCurrentSteeringPromotion(context.Context, string, string, string, string) (domain.OperatorSteeringPromotionInspection, error)
	}); ok {
		inspect = controller.InspectCurrentSteeringPromotion
	}
	result, err := inspect(request.Context(), sessionID, messageID, key, "http_session_operator")
	if err != nil {
		return nil, nil, err
	}
	view := SessionSteeringPromotionObservationView{Version: domain.SessionSteeringPromotionProtocolVersion, SessionID: sessionID, MessageID: messageID, State: string(result.State)}
	view.ExecutionObserved, view.ExecutionID = result.ExecutionObserved, result.ExecutionID
	if result.State == domain.OperatorSteeringRevisionSealed {
		r := result.Receipt
		if r == nil || r.Validate() != nil || r.SessionID != sessionID || r.MessageID != messageID || r.RequestedBy != "http_session_operator" {
			return nil, nil, apperror.New(apperror.CodeConflict, "promotion observation binding differs")
		}
		p := promotionView(*r, true)
		view.Promotion = &p
	} else if result.State == "rejected" && result.Receipt == nil && result.Rejection != nil {
		r := result.Rejection
		if r.Validate() != nil || r.SessionID != sessionID || r.MessageID != messageID || r.RequestedBy != "http_session_operator" {
			return nil, nil, apperror.New(apperror.CodeConflict, "promotion rejection observation binding differs")
		}
		p := promotionRejectionView(*r, true)
		view.Rejection = &p
	} else if result.State == domain.OperatorSteeringRevisionAbsent && result.Receipt == nil && result.Rejection == nil {
		view.Message, err = steeringObservationMessage(result.Message, sessionID, messageID)
		if err != nil {
			return nil, nil, err
		}
	} else {
		return nil, nil, apperror.New(apperror.CodeConflict, "promotion observation state differs")
	}
	return view, nil, nil
}
