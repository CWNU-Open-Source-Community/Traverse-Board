package application

import (
	"context"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
)

type standardCodeOperatorInputStore interface {
	SupervisorOperatorMessageID(context.Context, string, string, int) (string, error)
}

func (m *standardCodeSupervisorTurn) operatorMessage(ctx context.Context, attemptID string, turn int) (string, error) {
	reader, ok := m.store.(standardCodeOperatorInputStore)
	if !ok {
		return "", nil
	}
	return reader.SupervisorOperatorMessageID(ctx, m.turn.Run.ID, attemptID, turn)
}

func (m *standardCodeSupervisorTurn) bindOperatorInput(ctx context.Context) error {
	if !m.turn.OperatorSteering {
		return nil
	}
	id, err := m.operatorMessage(ctx, m.turn.Checkpoint.AttemptID, m.turn.Checkpoint.NextTurn)
	if err != nil {
		return apperror.Normalize(err)
	}
	if id == "" {
		return apperror.New(apperror.CodeFailedPrecondition, "Standard Code user input has no exact durable delivery")
	}
	m.operatorMessageID = id
	return nil
}

func (m *standardCodeSupervisorTurn) newOperatorInput(ctx context.Context, previous domain.StandardCodeSupervisorSnapshot) (bool, error) {
	if m.operatorMessageID == "" {
		return false, nil
	}
	id, err := m.operatorMessage(ctx, previous.AttemptID, previous.Turn)
	return err == nil && id != m.operatorMessageID, err
}

func (m *standardCodeSupervisorTurn) operatorCommandRetryAvailable() bool {
	if m.operatorMessageID == "" {
		return false
	}
	available := false
	for _, entry := range m.ledger {
		if entry.Snapshot.AttemptID != m.turn.Checkpoint.AttemptID || entry.Snapshot.Turn != m.turn.Checkpoint.NextTurn {
			continue
		}
		if entry.Kind == domain.StandardCodeSupervisorCallAuthorized &&
			(entry.ToolKind == domain.StandardCodeToolCommandRun || entry.ToolKind == domain.StandardCodeToolCommandStart) {
			return false
		}
		if entry.Kind == domain.StandardCodeSupervisorTurnPrepared && entry.ReasonCode == "operator_input_reopens_verification" {
			available = true
		}
	}
	return available
}

// Keep historical fingerprints unchanged. Only command launches belonging to
// a different, explicitly delivered user message leave the duplicate scope;
// file receipts and job-management side effects keep their existing scope.
func (m *standardCodeSupervisorTurn) commandIntentAlreadyHandled(ctx context.Context, d standardCodeCallDescriptor, callID string) (bool, error) {
	for _, entry := range m.ledger {
		if entry.ToolCallID == callID || entry.IntentFingerprint != d.Intent ||
			(entry.Kind != domain.StandardCodeSupervisorCallAuthorized && entry.Kind != domain.StandardCodeSupervisorCallObserved) {
			continue
		}
		if m.operatorMessageID != "" &&
			(d.Kind == domain.StandardCodeToolCommandRun || d.Kind == domain.StandardCodeToolCommandStart) {
			prior, err := m.operatorMessage(ctx, entry.Snapshot.AttemptID, entry.Snapshot.Turn)
			if err != nil {
				return false, apperror.Normalize(err)
			}
			if prior != m.operatorMessageID {
				continue
			}
		}
		return true, nil
	}
	return false, nil
}
