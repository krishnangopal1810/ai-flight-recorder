package service

import (
	"strconv"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

type payloadReference struct {
	path string
	ref  *types.PayloadRef
}

func validateCapturedPayloadReferences(capsule types.RunCapsule) error {
	declared := make(map[string]struct{}, len(capsule.Integrity.PayloadDigests))
	for _, digest := range capsule.Integrity.PayloadDigests {
		declared[digest] = struct{}{}
	}
	for digest, references := range capturedPayloadReferences(capsule) {
		if digest == "" {
			return types.NewError(types.ErrValidation, "captured payload reference has no digest", "restore a verified capsule manifest", nil)
		}
		if _, ok := declared[digest]; !ok {
			return types.NewError(types.ErrValidation, "captured payload reference is absent from capsule integrity", "restore a verified capsule manifest", nil)
		}
		if len(references) == 0 {
			return types.NewError(types.ErrValidation, "captured payload reference is invalid", "restore a verified capsule manifest", nil)
		}
	}
	return nil
}

func capturedPayloadReferences(capsule types.RunCapsule) map[string][]payloadReference {
	result := map[string][]payloadReference{}
	add := func(path string, ref *types.PayloadRef) {
		if ref == nil || ref.State == types.PayloadStateOmitted {
			return
		}
		result[ref.Digest] = append(result[ref.Digest], payloadReference{path: path, ref: ref})
	}
	for index := range capsule.Model.ToolDefinitions {
		add("model_configuration.tool_definitions["+strconv.Itoa(index)+"]", &capsule.Model.ToolDefinitions[index])
	}
	for index := range capsule.Operations {
		add("operations["+strconv.Itoa(index)+"].input", capsule.Operations[index].Input)
		add("operations["+strconv.Itoa(index)+"].output", capsule.Operations[index].Output)
	}
	for index := range capsule.Logs {
		add("logs["+strconv.Itoa(index)+"].body", capsule.Logs[index].Body)
		add("logs["+strconv.Itoa(index)+"].attributes", capsule.Logs[index].Attributes)
	}
	for index := range capsule.Artifacts {
		add("artifacts["+strconv.Itoa(index)+"].payload", capsule.Artifacts[index].Payload)
	}
	for index := range capsule.Boundaries {
		add("boundaries["+strconv.Itoa(index)+"].response", capsule.Boundaries[index].Response)
	}
	for evaluationIndex := range capsule.Evaluations {
		for inputIndex := range capsule.Evaluations[evaluationIndex].Inputs {
			add("evaluations["+strconv.Itoa(evaluationIndex)+"].inputs["+strconv.Itoa(inputIndex)+"]", &capsule.Evaluations[evaluationIndex].Inputs[inputIndex])
		}
		add("evaluations["+strconv.Itoa(evaluationIndex)+"].evidence", capsule.Evaluations[evaluationIndex].Evidence)
	}
	return result
}
