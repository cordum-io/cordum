import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Search, ChevronDown, ChevronRight, Package, Layers } from "lucide-react";
import { api } from "../../lib/api";
import { ALL_NODE_TYPES, NODE_CONFIGS } from "./nodes";
import type { BuilderNodeType, DragData, PackTopic } from "./types";

type Props = {
  onDragStart?: () => void;
  onDragEnd?: () => void;
};

export function BuilderSidebar({ onDragStart, onDragEnd }: Props) {
  const [searchTerm, setSearchTerm] = useState("");
  const [expandedSections, setExpandedSections] = useState<Record<string, boolean>>({
    nodes: true,
    packs: true,
  });

  const packsQuery = useQuery({
    queryKey: ["packs"],
    queryFn: () => api.listPacks(),
    staleTime: 60_000,
  });

  const toggleSection = (section: string) => {
    setExpandedSections((prev) => ({ ...prev, [section]: !prev[section] }));
  };

  const handleNodeDragStart = (
    e: React.DragEvent,
    nodeType: BuilderNodeType
  ) => {
    const data: DragData = { type: "node", nodeType };
    e.dataTransfer.setData("application/json", JSON.stringify(data));
    e.dataTransfer.effectAllowed = "copy";
    onDragStart?.();
  };

  const handlePackDragStart = (e: React.DragEvent, topic: PackTopic) => {
    const data: DragData = { type: "pack", topic };
    e.dataTransfer.setData("application/json", JSON.stringify(data));
    e.dataTransfer.effectAllowed = "copy";
    onDragStart?.();
  };

  const handleDragEnd = () => {
    onDragEnd?.();
  };

  // Extract topics from packs
  const packTopics: PackTopic[] = [];
  packsQuery.data?.items.forEach((pack) => {
    pack.manifest?.topics?.forEach((topic) => {
      if (topic.name) {
        packTopics.push({
          packId: pack.id,
          packTitle: pack.manifest?.metadata?.title || pack.id,
          topicName: topic.name,
          capability: topic.capability,
          riskTags: topic.riskTags,
          requires: topic.requires,
        });
      }
    });
  });

  // Filter by search
  const filteredNodes = ALL_NODE_TYPES.filter((type) =>
    NODE_CONFIGS[type].label.toLowerCase().includes(searchTerm.toLowerCase())
  );
  const filteredTopics = packTopics.filter(
    (t) =>
      t.topicName.toLowerCase().includes(searchTerm.toLowerCase()) ||
      t.packTitle?.toLowerCase().includes(searchTerm.toLowerCase())
  );

  return (
    <div className="builder-sidebar">
      {/* Search */}
      <div className="builder-sidebar__search">
        <Search className="h-4 w-4 text-muted" />
        <input
          type="text"
          placeholder="Search nodes..."
          value={searchTerm}
          onChange={(e) => setSearchTerm(e.target.value)}
          className="builder-sidebar__search-input"
        />
      </div>

      {/* Node Types Section */}
      <div className="builder-sidebar__section">
        <button
          className="builder-sidebar__section-header"
          onClick={() => toggleSection("nodes")}
        >
          {expandedSections.nodes ? (
            <ChevronDown className="h-4 w-4" />
          ) : (
            <ChevronRight className="h-4 w-4" />
          )}
          <Layers className="h-4 w-4" />
          <span>Node Types</span>
          <span className="builder-sidebar__section-count">{filteredNodes.length}</span>
        </button>

        {expandedSections.nodes && (
          <div className="builder-sidebar__section-content">
            {filteredNodes.map((type) => {
              const config = NODE_CONFIGS[type];
              return (
                <div
                  key={type}
                  draggable
                  onDragStart={(e) => handleNodeDragStart(e, type)}
                  onDragEnd={handleDragEnd}
                  className="builder-sidebar__item"
                >
                  <div className={`builder-sidebar__item-icon ${config.color}`}>
                    {config.icon}
                  </div>
                  <div className="builder-sidebar__item-info">
                    <div className="builder-sidebar__item-label">{config.label}</div>
                    <div className="builder-sidebar__item-desc">{config.description}</div>
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </div>

      {/* Pack Topics Section */}
      <div className="builder-sidebar__section">
        <button
          className="builder-sidebar__section-header"
          onClick={() => toggleSection("packs")}
        >
          {expandedSections.packs ? (
            <ChevronDown className="h-4 w-4" />
          ) : (
            <ChevronRight className="h-4 w-4" />
          )}
          <Package className="h-4 w-4" />
          <span>Pack Topics</span>
          <span className="builder-sidebar__section-count">{filteredTopics.length}</span>
        </button>

        {expandedSections.packs && (
          <div className="builder-sidebar__section-content">
            {packsQuery.isLoading ? (
              <div className="builder-sidebar__loading">Loading packs...</div>
            ) : filteredTopics.length === 0 ? (
              <div className="builder-sidebar__empty">No pack topics available</div>
            ) : (
              filteredTopics.map((topic, idx) => (
                <div
                  key={`${topic.packId}-${topic.topicName}-${idx}`}
                  draggable
                  onDragStart={(e) => handlePackDragStart(e, topic)}
                  onDragEnd={handleDragEnd}
                  className="builder-sidebar__item builder-sidebar__item--pack"
                >
                  <div className="builder-sidebar__item-icon bg-accent">WO</div>
                  <div className="builder-sidebar__item-info">
                    <div className="builder-sidebar__item-label">{topic.topicName}</div>
                    <div className="builder-sidebar__item-desc">
                      {topic.packTitle}
                      {topic.capability && ` - ${topic.capability}`}
                    </div>
                    {topic.riskTags && topic.riskTags.length > 0 && (
                      <div className="builder-sidebar__item-tags">
                        {topic.riskTags.map((tag) => (
                          <span key={tag} className="builder-sidebar__tag">
                            {tag}
                          </span>
                        ))}
                      </div>
                    )}
                  </div>
                </div>
              ))
            )}
          </div>
        )}
      </div>
    </div>
  );
}
