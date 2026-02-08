import { useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { Info, Layers, Plus, X } from "lucide-react";
import { Card } from "../components/ui/Card";
import { Button } from "../components/ui/Button";
import { Input } from "../components/ui/Input";
import { Drawer } from "../components/ui/Drawer";
import { EnvironmentCard } from "../components/settings/EnvironmentCard";
import { EnvironmentConfigEditor } from "../components/settings/EnvironmentConfigEditor";
import { PromotionDrawer } from "../components/settings/PromotionDrawer";
import { useEnvironments, useSaveEnvironment } from "../hooks/useSettings";
import type { Environment } from "../api/types";

// ---------------------------------------------------------------------------
// Add-environment modal schema
// ---------------------------------------------------------------------------

const addEnvSchema = z.object({
  name: z
    .string()
    .min(1, "Name is required")
    .max(32)
    .regex(/^[a-z0-9-]+$/, "Lowercase alphanumeric and dashes only"),
});

type AddEnvForm = z.infer<typeof addEnvSchema>;

// ---------------------------------------------------------------------------
// Page
// ---------------------------------------------------------------------------

export default function SettingsEnvironmentsPage() {
  const { data: environments, isLoading } = useEnvironments();
  const saveEnv = useSaveEnvironment();

  const [editEnv, setEditEnv] = useState<Environment | null>(null);
  const [promoteSource, setPromoteSource] = useState<Environment | null>(null);
  const [addOpen, setAddOpen] = useState(false);

  const isOnly = environments.length <= 1;

  // Find promotion target (always promote toward production)
  const promoteTarget =
    promoteSource && environments.find((e) => e.name === "production" && e.id !== promoteSource.id);

  const handlePromote = () => {
    if (!promoteSource || !promoteTarget) return;
    const updated: Environment = {
      ...promoteTarget,
      config: { ...promoteSource.config },
      lastPromotedAt: new Date().toISOString(),
    };
    saveEnv.mutate(updated, { onSuccess: () => setPromoteSource(null) });
  };

  // Loading skeleton
  if (isLoading) {
    return (
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {Array.from({ length: 3 }, (_, i) => (
          <div key={i} className="h-48 animate-pulse rounded-2xl bg-surface2" />
        ))}
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h2 className="font-display text-lg font-semibold text-ink">Environments</h2>
          <p className="text-xs text-muted">Manage deployment environments and configuration overrides</p>
        </div>
        <Button variant="primary" size="sm" type="button" onClick={() => setAddOpen(true)}>
          <Plus className="h-3.5 w-3.5" />
          Add Environment
        </Button>
      </div>

      {/* Info banner */}
      <div className="flex items-start gap-3 rounded-xl border border-accent/20 bg-accent/5 px-4 py-3">
        <Info className="mt-0.5 h-4 w-4 shrink-0 text-accent" />
        <p className="text-xs text-muted">
          Multi-environment support is configured locally. Connect environment backend to enable sync.
        </p>
      </div>

      {/* Single-environment empty state */}
      {isOnly && (
        <Card>
          <div className="flex flex-col items-center gap-3 py-8 text-center">
            <Layers className="h-10 w-10 text-muted opacity-40" />
            <h3 className="font-display text-base font-semibold text-ink">
              Single Environment Mode
            </h3>
            <p className="max-w-sm text-sm text-muted">
              You&apos;re running in single environment mode. Add a staging or development
              environment to enable promotion workflows and config comparison.
            </p>
            <Button variant="outline" size="sm" type="button" onClick={() => setAddOpen(true)}>
              <Plus className="h-3.5 w-3.5" />
              Add Staging Environment
            </Button>
          </div>
        </Card>
      )}

      {/* Environment cards */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {environments.map((env) => (
          <EnvironmentCard
            key={env.id}
            env={env}
            onEdit={setEditEnv}
            onPromote={setPromoteSource}
            isOnly={isOnly}
          />
        ))}
      </div>

      {/* Edit drawer */}
      <Drawer open={!!editEnv} onClose={() => setEditEnv(null)} size="lg">
        {editEnv && (
          <EnvironmentConfigEditor env={editEnv} onClose={() => setEditEnv(null)} />
        )}
      </Drawer>

      {/* Promotion drawer */}
      {promoteSource && promoteTarget && (
        <PromotionDrawer
          source={promoteSource}
          target={promoteTarget}
          onConfirm={handlePromote}
          onClose={() => setPromoteSource(null)}
          isPending={saveEnv.isPending}
        />
      )}

      {/* Add environment modal */}
      {addOpen && (
        <AddEnvironmentModal
          onClose={() => setAddOpen(false)}
          onAdd={(name) => {
            const newEnv: Environment = {
              id: name,
              name,
              status: "active",
              config: {},
            };
            saveEnv.mutate(newEnv, { onSuccess: () => setAddOpen(false) });
          }}
          isPending={saveEnv.isPending}
          existingNames={environments.map((e) => e.name)}
        />
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Add environment modal
// ---------------------------------------------------------------------------

function AddEnvironmentModal({
  onClose,
  onAdd,
  isPending,
  existingNames,
}: {
  onClose: () => void;
  onAdd: (name: string) => void;
  isPending: boolean;
  existingNames: string[];
}) {
  const {
    register,
    handleSubmit,
    formState: { errors },
  } = useForm<AddEnvForm>({
    resolver: zodResolver(addEnvSchema),
  });

  const onSubmit = (values: AddEnvForm) => {
    if (existingNames.includes(values.name)) return;
    onAdd(values.name);
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <form
        onSubmit={handleSubmit(onSubmit)}
        className="surface-card w-full max-w-md rounded-3xl p-6 shadow-xl"
      >
        <div className="mb-4 flex items-center justify-between">
          <h3 className="font-display text-lg font-semibold text-ink">Add Environment</h3>
          <button type="button" onClick={onClose} className="rounded-full p-1 hover:bg-surface2">
            <X className="h-4 w-4 text-muted" />
          </button>
        </div>

        <div className="space-y-3">
          <div className="space-y-1">
            <label className="text-xs font-semibold text-muted">Environment Name</label>
            <Input {...register("name")} placeholder="staging" />
            {errors.name && <p className="text-xs text-danger">{errors.name.message}</p>}
          </div>

          <div className="flex flex-wrap gap-2">
            {["staging", "development", "qa"].map((suggestion) =>
              existingNames.includes(suggestion) ? null : (
                <button
                  key={suggestion}
                  type="button"
                  onClick={() => onAdd(suggestion)}
                  className="rounded-full border border-border px-3 py-1 text-xs font-medium text-ink transition hover:border-accent hover:bg-accent/5"
                >
                  {suggestion}
                </button>
              ),
            )}
          </div>
        </div>

        <div className="mt-6 flex justify-end gap-3">
          <Button variant="ghost" size="sm" type="button" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" size="sm" type="submit" disabled={isPending}>
            <Plus className="h-3.5 w-3.5" />
            {isPending ? "Creating..." : "Create"}
          </Button>
        </div>
      </form>
    </div>
  );
}
