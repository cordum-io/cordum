package edge

import (
	"errors"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// PolicyMappingOptions carries trusted caller context that is not derivable
// from an AgentActionEvent.
type PolicyMappingOptions struct {
	ActorID   string
	ActorType pb.ActorType
}

// MapEventToPolicyCheckRequest maps a classified Edge action to the existing
// Safety Kernel PolicyCheckRequest wire shape.
func MapEventToPolicyCheckRequest(AgentActionEvent, ActionClassification, PolicyMappingOptions) (*pb.PolicyCheckRequest, error) {
	return nil, errors.New("edge policy mapper not implemented")
}
