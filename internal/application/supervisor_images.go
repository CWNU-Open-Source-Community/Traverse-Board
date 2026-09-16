package application

import (
	"context"
	"encoding/json"
	"fmt"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/session"
)

type supervisorImageStore interface {
	ListSupervisorImageEvidence(context.Context, domain.SupervisorCheckpoint, []int64) (domain.ModelImageContext, error)
	GetWorkspaceImage(context.Context, string, string) (domain.WorkspaceImage, []byte, error)
}

func (s *RunSupervisor) supervisorMessagesWithImages(ctx context.Context, turn domain.SupervisorTurn, history []session.Message, messages []llm.Message, layout modelContextLayout) ([]llm.Message, error) {
	store, ok := s.store.(supervisorImageStore)
	if !ok {
		if turn.Checkpoint.PendingImageCount > 0 {
			return nil, apperror.New(apperror.CodeFailedPrecondition, "Image input persistence is unavailable")
		}
		return messages, nil
	}
	historyIDs := make([]int64, 0, len(history))
	for _, message := range history {
		historyIDs = append(historyIDs, message.ID)
	}
	imageContext, err := store.ListSupervisorImageEvidence(ctx, turn.Checkpoint, historyIDs)
	if err != nil {
		return nil, err
	}
	if len(imageContext.Images) == 0 && imageContext.OmittedOlderImages == 0 {
		return messages, nil
	}
	positions := map[int64]int{}
	position := layout.HistoryStart
	for _, message := range history {
		projected := session.ProjectContextMessage(message)
		if projected.Role == "user" || projected.Role == "assistant" || projected.Role == "system" {
			positions[message.ID] = position
			position++
		}
	}
	inputIndex := len(messages) - 1
	ref, err := supervisorModelRef(s.router, turn.Run.Config.ModelRoute)
	if err != nil {
		return nil, err
	}
	vision := s.router.DescribeVision(ref)
	for _, image := range imageContext.Images {
		index, found := positions[image.SessionMessageID]
		if image.Current {
			index = inputIndex
			found = true
		}
		if vision.State != llm.VisionSupported {
			if image.Current {
				return nil, apperror.New(apperror.CodeFailedPrecondition, "Current image input requires a model with confirmed vision support")
			}
			found = false
		}
		if found {
			if messages[index].Role != "user" {
				return nil, apperror.New(apperror.CodeConflict, "Image source is not an operator input")
			}
			metadata, data, err := store.GetWorkspaceImage(ctx, image.Image.WorkspaceID, image.Image.ID)
			if err != nil {
				return nil, err
			}
			if metadata != image.Image {
				return nil, apperror.New(apperror.CodeConflict, "Image metadata changed before model input")
			}
			messages[index].Images = append(messages[index].Images, llm.ImagePart{MediaType: image.Image.MIMEType, Data: data, SHA256: image.Image.SHA256, Width: image.Image.Width, Height: image.Image.Height})
			continue
		}
		body, _ := json.Marshal(struct {
			Image                 domain.WorkspaceImage `json:"image"`
			PixelsIncluded        bool                  `json:"pixels_included"`
			InstructionAuthorized bool                  `json:"instruction_authorized"`
		}{image.Image, false, false})
		messages = append(messages, llm.Message{Role: "user", Content: "Earlier image descriptor only: its original pixels are preserved but are outside this request's visual history. Do not claim to see or remember the image from this descriptor; request that the operator reference the saved image again if visual detail is needed. This is non-authorizing evidence.\n" + string(body)})
	}
	if imageContext.OmittedOlderImages > 0 {
		messages = append(messages, llm.Message{Role: "user", Content: fmt.Sprintf("Image history coverage: %d older image references are not individually included in this request. Their original image bytes and message bindings remain saved in this Thread. Only recent descriptors are included for compacted image history; they contain no pixels or visual summary. Do not claim to see or remember omitted images; ask the operator to reference the saved image again when needed. This is non-authorizing evidence.", imageContext.OmittedOlderImages)})
	}
	return messages, nil
}
