package v1

import "testing"

func TestAPIMessagesAccessors(t *testing.T) {
	req := &SubmitJobRequest{
		Prompt:         "hello",
		Topic:          "job.test",
		AdapterId:      "adapter",
		Priority:       "interactive",
		OrgId:          "org",
		TeamId:         "team",
		ProjectId:      "proj",
		PrincipalId:    "principal",
		IdempotencyKey: "idem",
		ActorId:        "actor",
		ActorType:      "human",
		PackId:         "pack",
		Capability:     "cap",
		RiskTags:       []string{"r1"},
		Requires:       []string{"req"},
		Labels:         map[string]string{"k": "v"},
		MemoryId:       "mem",
	}
	if req.String() == "" {
		t.Fatalf("expected String output")
	}
	req.ProtoMessage()
	req.Descriptor()
	_ = req.ProtoReflect()
	if req.GetPrompt() != "hello" ||
		req.GetTopic() != "job.test" ||
		req.GetAdapterId() != "adapter" ||
		req.GetPriority() != "interactive" ||
		req.GetOrgId() != "org" ||
		req.GetTeamId() != "team" ||
		req.GetProjectId() != "proj" ||
		req.GetPrincipalId() != "principal" ||
		req.GetIdempotencyKey() != "idem" ||
		req.GetActorId() != "actor" ||
		req.GetActorType() != "human" ||
		req.GetPackId() != "pack" ||
		req.GetCapability() != "cap" ||
		len(req.GetRiskTags()) != 1 ||
		len(req.GetRequires()) != 1 ||
		req.GetLabels()["k"] != "v" ||
		req.GetMemoryId() != "mem" {
		t.Fatalf("unexpected getters")
	}
	req.Reset()
	if req.GetPrompt() != "" || req.GetLabels() != nil {
		t.Fatalf("expected reset fields")
	}
	var nilReq *SubmitJobRequest
	_ = nilReq.GetTopic()

	resp := &SubmitJobResponse{JobId: "job-1", TraceId: "trace-1"}
	if resp.String() == "" {
		t.Fatalf("expected String output")
	}
	resp.ProtoMessage()
	resp.Descriptor()
	_ = resp.ProtoReflect()
	if resp.GetJobId() != "job-1" || resp.GetTraceId() != "trace-1" {
		t.Fatalf("unexpected getters")
	}
	resp.Reset()
	if resp.GetJobId() != "" {
		t.Fatalf("expected reset response")
	}
	var nilResp *SubmitJobResponse
	_ = nilResp.GetTraceId()

	statusReq := &GetJobStatusRequest{JobId: "job-2"}
	statusReq.ProtoMessage()
	statusReq.Descriptor()
	_ = statusReq.ProtoReflect()
	if statusReq.GetJobId() != "job-2" {
		t.Fatalf("unexpected job id")
	}
	statusReq.Reset()
	if statusReq.GetJobId() != "" {
		t.Fatalf("expected reset request")
	}
	var nilStatusReq *GetJobStatusRequest
	_ = nilStatusReq.GetJobId()

	statusResp := &GetJobStatusResponse{JobId: "job-3", Status: "running", ResultPtr: "redis://res:3"}
	statusResp.ProtoMessage()
	statusResp.Descriptor()
	_ = statusResp.ProtoReflect()
	if statusResp.GetStatus() != "running" || statusResp.GetResultPtr() != "redis://res:3" {
		t.Fatalf("unexpected status")
	}
	statusResp.Reset()
	if statusResp.GetStatus() != "" {
		t.Fatalf("expected reset response")
	}
	var nilStatusResp *GetJobStatusResponse
	_ = nilStatusResp.GetJobId()
}

func TestContextMessagesAccessors(t *testing.T) {
	mode := ContextMode_CONTEXT_MODE_CHAT
	if mode.Enum() == nil {
		t.Fatalf("expected enum pointer")
	}
	if mode.String() == "" {
		t.Fatalf("expected enum string")
	}
	if mode.Number() == 0 {
		t.Fatalf("expected enum number")
	}
	_ = ContextMode_CONTEXT_MODE_RAW.Descriptor()
	_ = ContextMode_CONTEXT_MODE_RAG.Type()
	_, _ = ContextMode_CONTEXT_MODE_UNSPECIFIED.EnumDescriptor()

	msg := &ModelMessage{Role: "user", Content: "hello"}
	if msg.String() == "" {
		t.Fatalf("expected String output")
	}
	msg.ProtoMessage()
	msg.Descriptor()
	_ = msg.ProtoReflect()
	if msg.GetRole() != "user" || msg.GetContent() != "hello" {
		t.Fatalf("unexpected getters")
	}
	msg.Reset()
	if msg.GetRole() != "" {
		t.Fatalf("expected reset model message")
	}
	var nilMsg *ModelMessage
	_ = nilMsg.GetContent()

	bwReq := &BuildWindowRequest{
		MemoryId:        "mem",
		Mode:            mode,
		Model:           "model",
		LogicalPayload:  []byte("payload"),
		MaxInputTokens:  128,
		MaxOutputTokens: 64,
	}
	if bwReq.String() == "" {
		t.Fatalf("expected String output")
	}
	bwReq.ProtoMessage()
	bwReq.Descriptor()
	_ = bwReq.ProtoReflect()
	if bwReq.GetMemoryId() != "mem" ||
		bwReq.GetMode() != mode ||
		bwReq.GetModel() != "model" ||
		string(bwReq.GetLogicalPayload()) != "payload" ||
		bwReq.GetMaxInputTokens() != 128 ||
		bwReq.GetMaxOutputTokens() != 64 {
		t.Fatalf("unexpected getters")
	}
	bwReq.Reset()
	if bwReq.GetMemoryId() != "" {
		t.Fatalf("expected reset build window request")
	}
	var nilBwReq *BuildWindowRequest
	_ = nilBwReq.GetMaxInputTokens()

	bwResp := &BuildWindowResponse{Messages: []*ModelMessage{msg}, InputTokens: 42, OutputTokens: 7}
	bwResp.ProtoMessage()
	bwResp.Descriptor()
	_ = bwResp.ProtoReflect()
	if len(bwResp.GetMessages()) != 1 || bwResp.GetInputTokens() != 42 || bwResp.GetOutputTokens() != 7 {
		t.Fatalf("unexpected response getters")
	}
	bwResp.Reset()
	if bwResp.GetInputTokens() != 0 {
		t.Fatalf("expected reset build window response")
	}
	var nilBwResp *BuildWindowResponse
	_ = nilBwResp.GetMessages()

	upReq := &UpdateMemoryRequest{
		MemoryId:       "mem",
		LogicalPayload: []byte("logical"),
		ModelResponse:  []byte("model"),
		Mode:           mode,
	}
	upReq.ProtoMessage()
	upReq.Descriptor()
	_ = upReq.ProtoReflect()
	if upReq.GetMemoryId() != "mem" ||
		upReq.GetMode() != mode ||
		string(upReq.GetLogicalPayload()) != "logical" ||
		string(upReq.GetModelResponse()) != "model" {
		t.Fatalf("unexpected update memory request")
	}
	upReq.Reset()
	if upReq.GetMemoryId() != "" || upReq.GetMode() != ContextMode_CONTEXT_MODE_UNSPECIFIED {
		t.Fatalf("expected reset update memory request")
	}
	var nilUpReq *UpdateMemoryRequest
	_ = nilUpReq.GetMemoryId()

	upResp := &UpdateMemoryResponse{}
	upResp.ProtoMessage()
	upResp.Descriptor()
	_ = upResp.String()
	_ = upResp.ProtoReflect()
	upResp.Reset()
}
