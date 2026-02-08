import { useCallback } from "react";
import { Plus, FolderPlus, Trash2 } from "lucide-react";
import { Button } from "../ui/Button";
import { cn } from "../../lib/utils";
import { ConditionRow } from "./ConditionRow";
import {
  type Condition,
  type ConditionGroup,
  isConditionGroup,
  createCondition,
  createConditionGroup,
} from "./conditionTypes";

const MAX_DEPTH = 3;

interface ConditionGroupBuilderProps {
  group: ConditionGroup;
  onChange: (updated: ConditionGroup) => void;
  onRemove?: () => void;
  depth?: number;
}

export function ConditionGroupBuilder({
  group,
  onChange,
  onRemove,
  depth = 0,
}: ConditionGroupBuilderProps) {
  const toggleLogic = useCallback(() => {
    onChange({ ...group, logic: group.logic === "AND" ? "OR" : "AND" });
  }, [group, onChange]);

  const updateItem = useCallback(
    (index: number, updated: Condition | ConditionGroup) => {
      const next = group.conditions.map((item, i) =>
        i === index ? updated : item,
      );
      onChange({ ...group, conditions: next });
    },
    [group, onChange],
  );

  const removeItem = useCallback(
    (index: number) => {
      onChange({
        ...group,
        conditions: group.conditions.filter((_, i) => i !== index),
      });
    },
    [group, onChange],
  );

  const addCondition = useCallback(() => {
    onChange({
      ...group,
      conditions: [...group.conditions, createCondition()],
    });
  }, [group, onChange]);

  const addGroup = useCallback(() => {
    if (depth >= MAX_DEPTH) return;
    onChange({
      ...group,
      conditions: [
        ...group.conditions,
        createConditionGroup("AND", [createCondition()]),
      ],
    });
  }, [group, onChange, depth]);

  return (
    <div
      className={cn(
        "space-y-3 rounded-xl p-3",
        depth > 0 && "border-l-2 border-accent/30 bg-surface2/20 ml-4",
      )}
    >
      {/* Header: logic toggle + remove */}
      <div className="flex items-center gap-2">
        <button
          type="button"
          onClick={toggleLogic}
          className={cn(
            "rounded-full px-3 py-1 text-xs font-bold uppercase tracking-wider transition-colors",
            group.logic === "AND"
              ? "bg-accent/15 text-accent"
              : "bg-warning/15 text-warning",
          )}
        >
          {group.logic}
        </button>
        <span className="text-[11px] text-muted">
          {group.logic === "AND"
            ? "All conditions must match"
            : "Any condition can match"}
        </span>
        {onRemove && (
          <Button
            variant="ghost"
            size="sm"
            type="button"
            onClick={onRemove}
            className="ml-auto text-danger"
          >
            <Trash2 className="h-3.5 w-3.5" />
          </Button>
        )}
      </div>

      {/* Conditions list */}
      <div className="space-y-2">
        {group.conditions.map((item, i) =>
          isConditionGroup(item) ? (
            <ConditionGroupBuilder
              key={item.id}
              group={item}
              onChange={(updated) => updateItem(i, updated)}
              onRemove={() => removeItem(i)}
              depth={depth + 1}
            />
          ) : (
            <ConditionRow
              key={item.id}
              condition={item}
              onChange={(updated) => updateItem(i, updated)}
              onRemove={() => removeItem(i)}
            />
          ),
        )}
      </div>

      {/* Action buttons */}
      <div className="flex gap-2">
        <Button variant="outline" size="sm" type="button" onClick={addCondition}>
          <Plus className="h-3.5 w-3.5" />
          Condition
        </Button>
        {depth < MAX_DEPTH && (
          <Button variant="outline" size="sm" type="button" onClick={addGroup}>
            <FolderPlus className="h-3.5 w-3.5" />
            Group
          </Button>
        )}
      </div>
    </div>
  );
}
