package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/cordum/cordum/sdk/runtime"
	"github.com/nats-io/nats.go"
)

var (
	apiBase  = envOr("CORDUM_GATEWAY", "http://localhost:8081")
	apiKey   = envOr("CORDUM_API_KEY", "")
	tenantID = envOr("CORDUM_TENANT_ID", "default")
	natsURL  = envOr("NATS_URL", "nats://127.0.0.1:4222")
)

type demoWorker struct {
	ID       string
	Name     string
	PackID   string
	Topics   []string
	Capacity int
}

var demoWorkers = []demoWorker{
	{ID: "compliance-agent-1", Name: "Compliance Agent", PackID: "compliance", Topics: []string{"job.compliance.audit", "job.compliance.check"}, Capacity: 8},
	{ID: "compliance-agent-2", Name: "Compliance Agent", PackID: "compliance", Topics: []string{"job.compliance.audit", "job.compliance.check"}, Capacity: 8},
	{ID: "data-processor-1", Name: "Data Processor", PackID: "data-ops", Topics: []string{"job.data-ops.transform", "job.data-ops.validate"}, Capacity: 16},
	{ID: "data-processor-2", Name: "Data Processor", PackID: "data-ops", Topics: []string{"job.data-ops.transform", "job.data-ops.validate"}, Capacity: 16},
	{ID: "ml-inference-1", Name: "ML Inference", PackID: "ml-pipeline", Topics: []string{"job.ml.inference", "job.ml.predict"}, Capacity: 4},
	{ID: "ml-inference-2", Name: "ML Inference", PackID: "ml-pipeline", Topics: []string{"job.ml.inference", "job.ml.predict"}, Capacity: 4},
	{ID: "notification-agent", Name: "Notification Service", PackID: "notifications", Topics: []string{"job.notify.email", "job.notify.slack", "job.notify.webhook"}, Capacity: 32},
	{ID: "payment-processor", Name: "Payment Processor", PackID: "payments", Topics: []string{"job.payments.process", "job.payments.refund"}, Capacity: 8},
	{ID: "document-agent", Name: "Document Processor", PackID: "documents", Topics: []string{"job.docs.parse", "job.docs.generate"}, Capacity: 12},
	{ID: "api-gateway-agent", Name: "API Gateway", PackID: "gateway", Topics: []string{"job.gateway.route", "job.gateway.validate"}, Capacity: 64},
}

type workflow struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	OrgID string          `json:"org_id"`
	Steps map[string]step `json:"steps"`
}

type step struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Type      string            `json:"type"`
	Topic     string            `json:"topic,omitempty"`
	DependsOn []string          `json:"depends_on,omitempty"`
	Condition string            `json:"condition,omitempty"`
	Input     map[string]string `json:"input,omitempty"`
	Meta      map[string]any    `json:"meta,omitempty"`
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if apiKey == "" {
		log.Fatal("CORDUM_API_KEY is required")
	}

	log.Println("[demo] connecting to NATS...")
	nc, err := nats.Connect(natsURL, nats.Name("demo-orchestration"), nats.Timeout(5*time.Second))
	if err != nil {
		log.Fatalf("nats connect: %v", err)
	}
	defer func() { _ = nc.Drain() }()

	// Start heartbeats for all demo workers
	log.Println("[demo] starting 10 demo workers...")
	for _, w := range demoWorkers {
		worker := w
		go func() {
			heartbeatFn := func() ([]byte, error) {
				active := randInt(worker.Capacity / 2)
				return runtime.HeartbeatPayload(worker.ID, worker.PackID, active, worker.Capacity, 0)
			}
			if payload, err := heartbeatFn(); err == nil {
				_ = runtime.EmitHeartbeat(nc, payload)
			}
			runtime.HeartbeatLoop(ctx, nc, heartbeatFn)
		}()
		log.Printf("[demo] started worker %s (%s)", worker.ID, worker.Name)
	}

	// Create demo workflows
	log.Println("[demo] creating 5 demo workflows...")
	workflows := []workflow{
		{
			ID: "compliance.audit-flow", Name: "Compliance Audit Pipeline", OrgID: tenantID,
			Steps: map[string]step{
				"collect":  {ID: "collect", Name: "Collect Data", Type: "worker", Topic: "job.compliance.audit", Meta: map[string]any{"labels": map[string]string{"ui_node_type": "worker"}}},
				"validate": {ID: "validate", Name: "Validate Records", Type: "worker", Topic: "job.compliance.check", DependsOn: []string{"collect"}, Meta: map[string]any{"labels": map[string]string{"ui_node_type": "worker"}}},
				"approval": {ID: "approval", Name: "Manager Approval", Type: "condition", Condition: "true", DependsOn: []string{"validate"}, Meta: map[string]any{"labels": map[string]string{"ui_node_type": "approval"}}},
				"report":   {ID: "report", Name: "Generate Report", Type: "worker", Topic: "job.docs.generate", DependsOn: []string{"approval"}, Meta: map[string]any{"labels": map[string]string{"ui_node_type": "worker"}}},
			},
		},
		{
			ID: "data-ops.etl-pipeline", Name: "Data ETL Pipeline", OrgID: tenantID,
			Steps: map[string]step{
				"extract":   {ID: "extract", Name: "Extract Data", Type: "worker", Topic: "job.data-ops.transform", Meta: map[string]any{"labels": map[string]string{"ui_node_type": "worker"}}},
				"validate":  {ID: "validate", Name: "Validate Schema", Type: "worker", Topic: "job.data-ops.validate", DependsOn: []string{"extract"}, Meta: map[string]any{"labels": map[string]string{"ui_node_type": "worker"}}},
				"transform": {ID: "transform", Name: "Transform Records", Type: "worker", Topic: "job.data-ops.transform", DependsOn: []string{"validate"}, Meta: map[string]any{"labels": map[string]string{"ui_node_type": "worker"}}},
				"notify":    {ID: "notify", Name: "Send Completion Notice", Type: "worker", Topic: "job.notify.slack", DependsOn: []string{"transform"}, Meta: map[string]any{"labels": map[string]string{"ui_node_type": "worker"}}},
			},
		},
		{
			ID: "ml-pipeline.prediction", Name: "ML Prediction Pipeline", OrgID: tenantID,
			Steps: map[string]step{
				"preprocess": {ID: "preprocess", Name: "Preprocess Input", Type: "worker", Topic: "job.data-ops.transform", Meta: map[string]any{"labels": map[string]string{"ui_node_type": "worker"}}},
				"inference":  {ID: "inference", Name: "Run Inference", Type: "worker", Topic: "job.ml.inference", DependsOn: []string{"preprocess"}, Meta: map[string]any{"labels": map[string]string{"ui_node_type": "worker"}}},
				"review":     {ID: "review", Name: "Human Review", Type: "condition", Condition: "input.confidence < 0.9", DependsOn: []string{"inference"}, Meta: map[string]any{"labels": map[string]string{"ui_node_type": "approval"}}},
				"output":     {ID: "output", Name: "Deliver Results", Type: "worker", Topic: "job.notify.webhook", DependsOn: []string{"inference"}, Condition: "input.confidence >= 0.9", Meta: map[string]any{"labels": map[string]string{"ui_node_type": "worker"}}},
			},
		},
		{
			ID: "payments.refund-flow", Name: "Payment Refund Workflow", OrgID: tenantID,
			Steps: map[string]step{
				"request":  {ID: "request", Name: "Refund Request", Type: "worker", Topic: "job.payments.process", Meta: map[string]any{"labels": map[string]string{"ui_node_type": "worker"}}},
				"verify":   {ID: "verify", Name: "Verify Transaction", Type: "worker", Topic: "job.compliance.check", DependsOn: []string{"request"}, Meta: map[string]any{"labels": map[string]string{"ui_node_type": "worker"}}},
				"approval": {ID: "approval", Name: "Finance Approval", Type: "condition", Condition: "input.amount > 100", DependsOn: []string{"verify"}, Meta: map[string]any{"labels": map[string]string{"ui_node_type": "approval"}}},
				"process":  {ID: "process", Name: "Process Refund", Type: "worker", Topic: "job.payments.refund", DependsOn: []string{"approval"}, Meta: map[string]any{"labels": map[string]string{"ui_node_type": "worker"}}},
				"notify":   {ID: "notify", Name: "Customer Notification", Type: "worker", Topic: "job.notify.email", DependsOn: []string{"process"}, Meta: map[string]any{"labels": map[string]string{"ui_node_type": "worker"}}},
			},
		},
		{
			ID: "documents.review-process", Name: "Document Review Process", OrgID: tenantID,
			Steps: map[string]step{
				"ingest":   {ID: "ingest", Name: "Ingest Document", Type: "worker", Topic: "job.docs.parse", Meta: map[string]any{"labels": map[string]string{"ui_node_type": "worker"}}},
				"classify": {ID: "classify", Name: "ML Classification", Type: "worker", Topic: "job.ml.predict", DependsOn: []string{"ingest"}, Meta: map[string]any{"labels": map[string]string{"ui_node_type": "worker"}}},
				"review":   {ID: "review", Name: "Legal Review", Type: "condition", Condition: "input.sensitivity == 'high'", DependsOn: []string{"classify"}, Meta: map[string]any{"labels": map[string]string{"ui_node_type": "approval"}}},
				"archive":  {ID: "archive", Name: "Archive Document", Type: "worker", Topic: "job.docs.generate", DependsOn: []string{"classify"}, Condition: "input.sensitivity != 'high'", Meta: map[string]any{"labels": map[string]string{"ui_node_type": "worker"}}},
			},
		},
	}

	for _, wf := range workflows {
		if err := createWorkflow(wf); err != nil {
			log.Printf("[demo] warning: failed to create workflow %s: %v", wf.ID, err)
		} else {
			log.Printf("[demo] created workflow: %s", wf.Name)
		}
	}

	// Submit demo jobs
	log.Println("[demo] submitting demo jobs...")
	demoJobs := []struct {
		Topic   string
		Payload map[string]any
	}{
		// Running jobs (auto-approved)
		{Topic: "job.data-ops.transform", Payload: map[string]any{"source": "s3://data/input.csv", "target": "s3://data/output.parquet"}},
		{Topic: "job.data-ops.transform", Payload: map[string]any{"source": "s3://data/batch2.csv", "target": "s3://data/batch2.parquet"}},
		{Topic: "job.ml.inference", Payload: map[string]any{"model": "fraud-detector-v2", "inputs": []string{"tx_001", "tx_002"}, "confidence": 0.95}},
		{Topic: "job.notify.slack", Payload: map[string]any{"channel": "#alerts", "message": "Pipeline completed successfully"}},
		{Topic: "job.docs.parse", Payload: map[string]any{"document_id": "DOC-2024-001", "format": "pdf"}},
		// Jobs requiring approval
		{Topic: "job.demo-mock-bank.transfer.review", Payload: map[string]any{"amount": 15000, "currency": "USD", "customer": "Enterprise Corp", "reason": "Invoice payment", "policy_bucket": "review"}},
		{Topic: "job.demo-mock-bank.transfer.review", Payload: map[string]any{"amount": 8500, "currency": "EUR", "customer": "Global Ltd", "reason": "Refund processing", "policy_bucket": "review"}},
		{Topic: "job.payments.refund", Payload: map[string]any{"transaction_id": "TXN-8829", "amount": 450, "reason": "Customer request"}},
		// More running jobs
		{Topic: "job.compliance.audit", Payload: map[string]any{"scope": "quarterly", "department": "finance"}},
		{Topic: "job.gateway.validate", Payload: map[string]any{"request_id": "req-12345", "method": "POST", "path": "/api/v1/orders"}},
	}

	for i, job := range demoJobs {
		if err := submitJob(job.Topic, job.Payload); err != nil {
			log.Printf("[demo] warning: failed to submit job %d: %v", i+1, err)
		} else {
			log.Printf("[demo] submitted job to %s", job.Topic)
		}
		time.Sleep(500 * time.Millisecond)
	}

	log.Println("")
	log.Println("============================================")
	log.Println("Demo system ready!")
	log.Println("============================================")
	log.Println("")
	log.Println("Dashboard:    http://localhost:8082")
	log.Println("Mock Bank UI: http://localhost:8099")
	log.Println("API Gateway:  http://localhost:8081")
	log.Println("")
	log.Println("Workers:      10 agents connected")
	log.Println("Workflows:    5 pipelines created")
	log.Println("Jobs:         Running and pending approval")
	log.Println("")
	log.Println("Press Ctrl+C to stop...")

	<-ctx.Done()
}

func randInt(max int) int {
	if max <= 0 {
		return 0
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max)))
	if err != nil {
		return 0
	}
	return int(n.Int64())
}

func createWorkflow(wf workflow) error {
	body, _ := json.Marshal(wf)
	req, _ := http.NewRequest("POST", apiBase+"/api/v1/workflows", bytes.NewReader(body))
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("X-Tenant-ID", tenantID)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 && resp.StatusCode != 409 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

func submitJob(topic string, payload map[string]any) error {
	body, _ := json.Marshal(map[string]any{
		"topic":   topic,
		"payload": payload,
	})
	req, _ := http.NewRequest("POST", apiBase+"/api/v1/jobs", bytes.NewReader(body))
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("X-Tenant-ID", tenantID)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

func envOr(key, fallback string) string {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		return val
	}
	return fallback
}
