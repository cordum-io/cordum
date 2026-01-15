import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { X, Database, FileJson, ChevronDown, ChevronRight, Copy, Check } from "lucide-react";
import { api } from "../../lib/api";
import { Button } from "../ui/Button";
import type { StepRun } from "../../types/api";

type Props = {
  stepRun: StepRun | null;
  onClose: () => void;
};

export function StepOutputViewer({ stepRun, onClose }: Props) {
  const [expandedSections, setExpandedSections] = useState<Record<string, boolean>>({
    input: true,
    output: true,
    error: true,
  });
  const [copied, setCopied] = useState<string | null>(null);

  // Fetch memory/result if we have a result_ptr from the job
  const jobQuery = useQuery({
    queryKey: ["job", stepRun?.job_id],
    queryFn: () => api.getJob(stepRun!.job_id!),
    enabled: Boolean(stepRun?.job_id),
  });

  const memoryQuery = useQuery({
    queryKey: ["memory", jobQuery.data?.result_ptr],
    queryFn: () => api.getMemory(jobQuery.data!.result_ptr),
    enabled: Boolean(jobQuery.data?.result_ptr),
  });

  const contextQuery = useQuery({
    queryKey: ["memory", jobQuery.data?.context_ptr],
    queryFn: () => api.getMemory(jobQuery.data!.context_ptr),
    enabled: Boolean(jobQuery.data?.context_ptr),
  });

  const toggleSection = (section: string) => {
    setExpandedSections((prev) => ({ ...prev, [section]: !prev[section] }));
  };

  const copyToClipboard = (text: string, key: string) => {
    navigator.clipboard.writeText(text);
    setCopied(key);
    setTimeout(() => setCopied(null), 2000);
  };

  if (!stepRun) {
    return (
      <div className="step-output-viewer step-output-viewer--empty">
        <div className="step-output-viewer__placeholder">
          <Database className="h-8 w-8 text-muted" />
          <div className="text-sm text-muted mt-2">Select a step to view output</div>
        </div>
      </div>
    );
  }

  const renderJson = (data: unknown, key: string) => {
    const text = JSON.stringify(data, null, 2);
    return (
      <div className="step-output-viewer__json">
        <button
          className="step-output-viewer__copy"
          onClick={() => copyToClipboard(text, key)}
        >
          {copied === key ? (
            <Check className="h-3 w-3 text-success" />
          ) : (
            <Copy className="h-3 w-3" />
          )}
        </button>
        <pre>{text}</pre>
      </div>
    );
  };

  return (
    <div className="step-output-viewer">
      <div className="step-output-viewer__header">
        <div className="step-output-viewer__title">
          <FileJson className="h-4 w-4" />
          <span>Step: {stepRun.step_id}</span>
        </div>
        <button onClick={onClose} className="step-output-viewer__close">
          <X className="h-4 w-4" />
        </button>
      </div>

      <div className="step-output-viewer__meta">
        <div className="step-output-viewer__meta-item">
          <span className="step-output-viewer__meta-label">Status:</span>
          <span className={`step-output-viewer__status step-output-viewer__status--${stepRun.status}`}>
            {stepRun.status}
          </span>
        </div>
        {stepRun.job_id && (
          <div className="step-output-viewer__meta-item">
            <span className="step-output-viewer__meta-label">Job:</span>
            <span className="step-output-viewer__meta-value">{stepRun.job_id}</span>
          </div>
        )}
        {stepRun.attempts && (
          <div className="step-output-viewer__meta-item">
            <span className="step-output-viewer__meta-label">Attempts:</span>
            <span className="step-output-viewer__meta-value">{stepRun.attempts}</span>
          </div>
        )}
      </div>

      <div className="step-output-viewer__content">
        {/* Input Section */}
        {stepRun.input && (
          <div className="step-output-viewer__section">
            <button
              className="step-output-viewer__section-header"
              onClick={() => toggleSection("input")}
            >
              {expandedSections.input ? (
                <ChevronDown className="h-4 w-4" />
              ) : (
                <ChevronRight className="h-4 w-4" />
              )}
              <span>Input</span>
            </button>
            {expandedSections.input && renderJson(stepRun.input, "input")}
          </div>
        )}

        {/* Output Section */}
        {stepRun.output !== undefined && (
          <div className="step-output-viewer__section">
            <button
              className="step-output-viewer__section-header"
              onClick={() => toggleSection("output")}
            >
              {expandedSections.output ? (
                <ChevronDown className="h-4 w-4" />
              ) : (
                <ChevronRight className="h-4 w-4" />
              )}
              <span>Output</span>
            </button>
            {expandedSections.output && renderJson(stepRun.output, "output")}
          </div>
        )}

        {/* Memory Result Section */}
        {memoryQuery.data && (
          <div className="step-output-viewer__section">
            <button
              className="step-output-viewer__section-header"
              onClick={() => toggleSection("memory")}
            >
              {expandedSections.memory ? (
                <ChevronDown className="h-4 w-4" />
              ) : (
                <ChevronRight className="h-4 w-4" />
              )}
              <span>Memory Result</span>
              <span className="step-output-viewer__section-size">
                {memoryQuery.data.size_bytes} bytes
              </span>
            </button>
            {expandedSections.memory && (
              memoryQuery.data.json
                ? renderJson(memoryQuery.data.json, "memory")
                : memoryQuery.data.text
                ? <pre className="step-output-viewer__text">{memoryQuery.data.text}</pre>
                : <div className="step-output-viewer__binary">Binary data ({memoryQuery.data.size_bytes} bytes)</div>
            )}
          </div>
        )}

        {/* Context Section */}
        {contextQuery.data && (
          <div className="step-output-viewer__section">
            <button
              className="step-output-viewer__section-header"
              onClick={() => toggleSection("context")}
            >
              {expandedSections.context ? (
                <ChevronDown className="h-4 w-4" />
              ) : (
                <ChevronRight className="h-4 w-4" />
              )}
              <span>Context</span>
            </button>
            {expandedSections.context && (
              contextQuery.data.json
                ? renderJson(contextQuery.data.json, "context")
                : contextQuery.data.text
                ? <pre className="step-output-viewer__text">{contextQuery.data.text}</pre>
                : <div className="step-output-viewer__binary">Binary data</div>
            )}
          </div>
        )}

        {/* Error Section */}
        {stepRun.error && (
          <div className="step-output-viewer__section step-output-viewer__section--error">
            <button
              className="step-output-viewer__section-header"
              onClick={() => toggleSection("error")}
            >
              {expandedSections.error ? (
                <ChevronDown className="h-4 w-4" />
              ) : (
                <ChevronRight className="h-4 w-4" />
              )}
              <span>Error</span>
            </button>
            {expandedSections.error && renderJson(stepRun.error, "error")}
          </div>
        )}
      </div>

      <div className="step-output-viewer__footer">
        <Button variant="outline" size="sm" onClick={onClose}>
          Close
        </Button>
      </div>
    </div>
  );
}
