package application

import (
	"context"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
)

type specialistModelNotDispatchedStore interface {
	RecordSpecialistModelNotDispatched(context.Context, domain.AgentAttemptRef, llm.ModelAttempt) (domain.AgentAttempt, error)
}

func (r *SubagentRunner) recordSpecialistFailureAccounting(ctx context.Context, ref domain.AgentAttemptRef,
	attempt llm.ModelAttempt, usage *llm.Usage, notDispatched bool,
) (domain.AgentAttempt, error) {
	eventCtx, cancel := specialistEventContext(ctx)
	defer cancel()
	var charged domain.AgentAttempt
	var err error
	unsentReceipt := false
	if writer, ok := r.store.(specialistModelNotDispatchedStore); ok && notDispatched {
		charged, err = writer.RecordSpecialistModelNotDispatched(eventCtx, ref, attempt)
		unsentReceipt = err == nil
	} else {
		charged, err = r.store.RecordSpecialistModelFailed(eventCtx, ref, attempt, usage)
	}
	if err != nil || r.monetary == nil {
		return charged, err
	}
	if unsentReceipt {
		_, err = r.monetary.ReleaseModelCall(eventCtx, ref.RunID, domain.MonetaryScopeSpecialist, attempt)
	} else if usage != nil {
		_, err = r.monetary.SettleModelCall(eventCtx, ref.RunID, domain.MonetaryScopeSpecialist, attempt, *usage, 0)
	} else {
		_, err = r.monetary.SettleUnknownModelCall(eventCtx, ref.RunID, domain.MonetaryScopeSpecialist, attempt)
	}
	return charged, err
}
