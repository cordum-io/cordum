import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { Card, CardHeader, CardTitle } from "../components/ui/Card";
import { Button } from "../components/ui/Button";
import { Input } from "../components/ui/Input";
import { Select } from "../components/ui/Select";
import { Textarea } from "../components/ui/Textarea";
import { Badge } from "../components/ui/Badge";
import { formatDateTime } from "../lib/format";

export function ToolsPage() {
  const [activeTab, setActiveTab] = useState<"artifacts" | "locks" | "memory">("artifacts");

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>System Tools</CardTitle>
          <div className="flex flex-wrap gap-2">
            <Button variant={activeTab === "artifacts" ? "primary" : "outline"} size="sm" onClick={() => setActiveTab("artifacts")}>Artifacts</Button>
            <Button variant={activeTab === "locks" ? "primary" : "outline"} size="sm" onClick={() => setActiveTab("locks")}>Locks</Button>
            <Button variant={activeTab === "memory" ? "primary" : "outline"} size="sm" onClick={() => setActiveTab("memory")}>Memory</Button>
          </div>
        </CardHeader>
      </Card>

      {activeTab === "artifacts" && <ArtifactsTool />}
      {activeTab === "locks" && <LocksTool />}
      {activeTab === "memory" && <MemoryTool />}
    </div>
  );
}

function ArtifactsTool() {
  const [ptr, setPtr] = useState("");
  const [lookupPtr, setLookupPtr] = useState("");
  const [uploadContent, setUploadContent] = useState("");
  const [contentType, setContentType] = useState("text/plain");

  const getQuery = useQuery({
    queryKey: ["artifact", lookupPtr],
    queryFn: () => api.getArtifact(lookupPtr),
    enabled: Boolean(lookupPtr),
    retry: false,
  });

  const uploadMutation = useMutation({
    mutationFn: () => api.putArtifact(uploadContent, contentType),
    onSuccess: (data) => {
      setPtr(data.artifact_ptr);
      setUploadContent("");
    },
  });
  let decodedContent = "";
  if (getQuery.data?.content_base64) {
    try {
      decodedContent = atob(getQuery.data.content_base64);
    } catch {
      decodedContent = "";
    }
  }

  return (
    <div className="grid gap-6 lg:grid-cols-2">
      <Card>
        <CardHeader>
          <CardTitle>Lookup Artifact</CardTitle>
          <div className="text-xs text-muted">View artifact content by pointer</div>
        </CardHeader>
        <div className="flex gap-2">
          <Input 
            value={ptr} 
            onChange={(e) => setPtr(e.target.value)} 
            placeholder="blob:sha256:..." 
          />
          <Button 
            variant="primary" 
            onClick={() => setLookupPtr(ptr)}
            disabled={!ptr}
          >
            Fetch
          </Button>
        </div>
        {getQuery.isError ? (
          <div className="mt-4 text-sm text-danger">Artifact not found or access denied.</div>
        ) : null}
        {getQuery.data ? (
          <div className="mt-4 space-y-3">
            <div className="rounded-2xl border border-border bg-white/70 p-3 text-xs text-muted">
              <div>Type: {getQuery.data.metadata?.content_type || "unknown"}</div>
              <div>Retention: {getQuery.data.metadata?.retention || "standard"}</div>
              <div>Size: {getQuery.data.content_base64?.length || 0} bytes (encoded)</div>
            </div>
            <Textarea readOnly rows={10} value={decodedContent} />
          </div>
        ) : null}
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Upload Artifact</CardTitle>
          <div className="text-xs text-muted">Manually store content</div>
        </CardHeader>
        <div className="space-y-3">
          <div>
            <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Content Type</label>
            <Input value={contentType} onChange={(e) => setContentType(e.target.value)} />
          </div>
          <div>
            <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Content</label>
            <Textarea 
              rows={8} 
              value={uploadContent} 
              onChange={(e) => setUploadContent(e.target.value)} 
              placeholder="Paste content here..."
            />
          </div>
          <Button 
            variant="primary" 
            onClick={() => uploadMutation.mutate()}
            disabled={!uploadContent || uploadMutation.isPending}
          >
            {uploadMutation.isPending ? "Uploading..." : "Store Artifact"}
          </Button>
          {uploadMutation.isSuccess ? (
             <div className="text-sm text-success">
               Stored successfully! Pointer: <span className="font-mono text-ink">{uploadMutation.data.artifact_ptr}</span>
             </div>
          ) : null}
        </div>
      </Card>
    </div>
  );
}

function LocksTool() {
  const [resource, setResource] = useState("");
  const [owner, setOwner] = useState("admin-tool");
  const [ttl, setTtl] = useState("30000");
  const [lookupResource, setLookupResource] = useState("");
  const ttlValue = Number.parseInt(ttl, 10);
  const ttlMs = Number.isFinite(ttlValue) ? ttlValue : 0;
  const ttlInvalid = ttlMs <= 0;

  const getQuery = useQuery({
    queryKey: ["lock", lookupResource],
    queryFn: () => api.getLock(lookupResource),
    enabled: Boolean(lookupResource),
    retry: false,
  });

  const acquireMutation = useMutation({
    mutationFn: () => api.acquireLock(resource, owner, ttlMs),
    onSuccess: () => setLookupResource(resource),
  });

  const releaseMutation = useMutation({
    mutationFn: () => api.releaseLock(resource, owner),
    onSuccess: () => setLookupResource(resource),
  });

  return (
    <div className="grid gap-6 lg:grid-cols-2">
      <Card>
        <CardHeader>
          <CardTitle>Manage Lock</CardTitle>
        </CardHeader>
        <div className="space-y-3">
          <div>
            <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Resource Key</label>
            <Input value={resource} onChange={(e) => setResource(e.target.value)} placeholder="my-resource-lock" />
          </div>
          <div>
            <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Owner ID</label>
            <Input value={owner} onChange={(e) => setOwner(e.target.value)} />
          </div>
          <div>
            <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">TTL (ms)</label>
            <Input type="number" value={ttl} onChange={(e) => setTtl(e.target.value)} />
          </div>
          <div className="flex gap-2 pt-2">
            <Button 
              variant="outline" 
              onClick={() => setLookupResource(resource)}
              disabled={!resource}
            >
              Check Status
            </Button>
            <Button 
              variant="primary" 
              onClick={() => acquireMutation.mutate()}
              disabled={!resource || ttlInvalid || acquireMutation.isPending}
            >
              Acquire
            </Button>
            <Button 
              variant="danger" 
              onClick={() => releaseMutation.mutate()}
              disabled={!resource || releaseMutation.isPending}
            >
              Release
            </Button>
          </div>
        </div>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Lock Status</CardTitle>
        </CardHeader>
        {getQuery.isError ? (
          <div className="text-sm text-muted">Lock is currently free (not found).</div>
        ) : null}
        {getQuery.data ? (
          <div className="space-y-3 rounded-2xl border border-border bg-white/70 p-4">
            <div className="flex items-center justify-between">
              <div className="text-sm font-semibold text-ink">{getQuery.data.resource}</div>
              <Badge variant="warning">LOCKED</Badge>
            </div>
            <div className="text-xs text-muted">
              <div>Owner: {getQuery.data.owner}</div>
              <div>Expires: {formatDateTime(getQuery.data.expires_at)}</div>
              <div>Mode: {getQuery.data.mode}</div>
            </div>
          </div>
        ) : !getQuery.isError && lookupResource ? (
           <div className="text-sm text-muted">Loading...</div>
        ) : (
           <div className="text-sm text-muted">Enter a resource key to check status.</div>
        )}
      </Card>
    </div>
  );
}

function MemoryTool() {
  const [input, setInput] = useState("");
  const [type, setType] = useState<"ptr" | "key">("ptr");
  const [lookup, setLookup] = useState<{ val: string; type: "ptr" | "key" } | null>(null);

  const getQuery = useQuery({
    queryKey: ["memory", lookup],
    queryFn: () => api.getMemory(
      lookup?.type === "ptr" ? lookup.val : undefined, 
      lookup?.type === "key" ? lookup.val : undefined
    ),
    enabled: Boolean(lookup),
    retry: false,
  });

  return (
    <Card>
      <CardHeader>
        <CardTitle>Memory Inspector</CardTitle>
        <div className="text-xs text-muted">Inspect raw Redis keys or Cordum pointers (ctx:*, res:*)</div>
      </CardHeader>
      <div className="flex gap-2">
        <Select value={type} onChange={(e) => setType(e.target.value as "ptr" | "key")} className="w-32">
          <option value="ptr">Pointer</option>
          <option value="key">Redis Key</option>
        </Select>
        <Input 
          value={input} 
          onChange={(e) => setInput(e.target.value)} 
          placeholder={type === "ptr" ? "ctx:..." : "cordum:..."} 
        />
        <Button 
          variant="primary" 
          onClick={() => setLookup({ val: input, type })}
          disabled={!input}
        >
          Inspect
        </Button>
      </div>

      <div className="mt-6">
        {getQuery.isLoading ? <div className="text-sm text-muted">Loading...</div> : null}
        {getQuery.isError ? <div className="text-sm text-danger">Not found or invalid key.</div> : null}
        {getQuery.data ? (
          <div className="space-y-4">
            <div className="grid gap-4 lg:grid-cols-3">
              <div className="rounded-2xl border border-border bg-white/70 p-3">
                <div className="text-xs uppercase tracking-[0.2em] text-muted">Kind</div>
                <div className="mt-1 text-sm font-semibold text-ink uppercase">{getQuery.data.kind}</div>
              </div>
              <div className="rounded-2xl border border-border bg-white/70 p-3">
                <div className="text-xs uppercase tracking-[0.2em] text-muted">Key</div>
                <div className="mt-1 text-xs font-mono text-ink truncate" title={getQuery.data.key}>{getQuery.data.key}</div>
              </div>
              <div className="rounded-2xl border border-border bg-white/70 p-3">
                <div className="text-xs uppercase tracking-[0.2em] text-muted">Size</div>
                <div className="mt-1 text-sm font-semibold text-ink">{getQuery.data.size_bytes} bytes</div>
              </div>
            </div>
            
            <div>
              <div className="mb-2 text-xs font-semibold uppercase tracking-[0.2em] text-muted">Content</div>
              <Textarea 
                readOnly 
                rows={16} 
                value={
                  getQuery.data.json 
                    ? JSON.stringify(getQuery.data.json, null, 2) 
                    : getQuery.data.text || getQuery.data.base64
                } 
                className="font-mono text-xs"
              />
            </div>
          </div>
        ) : null}
      </div>
    </Card>
  );
}
