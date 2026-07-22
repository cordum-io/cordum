package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"strings"

	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func newWorkflowOutputEvaluation(
	res *pb.JobResult,
	request *pb.JobRequest,
	output resolvedStepOutput,
) *model.OutputEvaluateRequest {
	evaluation := &model.OutputEvaluateRequest{
		JobID: strings.TrimSpace(res.GetJobId()), Topic: strings.TrimSpace(request.GetTopic()),
		Tenant: strings.TrimSpace(request.GetTenantId()), Labels: maps.Clone(request.GetLabels()),
		ArtifactPtrs: append([]string(nil), res.GetArtifactPtrs()...),
		ErrorMessage: strings.TrimSpace(res.GetErrorMessage()), ErrorCode: strings.TrimSpace(res.GetErrorCode()),
		WorkerID: strings.TrimSpace(res.GetWorkerId()), ExecutionMs: res.GetExecutionMs(),
		OutputSizeBytes: int64(len(output.content)), ContentHash: workflowOutputContentHash(output.content),
		WorkflowID:    strings.TrimSpace(request.GetWorkflowId()),
		StepID:        strings.TrimSpace(request.GetLabels()["step_id"]),
		OutputContent: append([]byte(nil), output.content...),
		PrincipalID:   strings.TrimSpace(request.GetPrincipalId()), ContentType: output.mediaType,
		OriginalLabels: maps.Clone(request.GetLabels()),
	}
	populateWorkflowOutputMetadata(evaluation, request)
	return evaluation
}

func populateWorkflowOutputMetadata(evaluation *model.OutputEvaluateRequest, request *pb.JobRequest) {
	meta := request.GetMeta()
	if meta == nil {
		return
	}
	if capability := strings.TrimSpace(meta.GetCapability()); capability != "" {
		evaluation.Capabilities = append(evaluation.Capabilities, capability)
	}
	evaluation.Capabilities = append(evaluation.Capabilities, meta.GetRequires()...)
	evaluation.RiskTags = append(evaluation.RiskTags, meta.GetRiskTags()...)
	evaluation.PackID = strings.TrimSpace(meta.GetPackId())
	if len(evaluation.OriginalLabels) == 0 && len(meta.GetLabels()) > 0 {
		evaluation.OriginalLabels = maps.Clone(meta.GetLabels())
	}
}

func workflowOutputContentHash(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}
