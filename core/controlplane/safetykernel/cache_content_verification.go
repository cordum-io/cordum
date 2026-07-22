package safetykernel

import (
	"crypto/sha256"
	"crypto/subtle"
	"strings"
	"time"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// decisionCacheContentVerified reports whether every content-bearing input
// that can affect this evaluation is complete and integrity-bound.
func decisionCacheContentVerified(req *pb.PolicyCheckRequest, contentSensitive bool) bool {
	if req == nil {
		return false
	}
	if req.GetInputRef() != nil && !referencedInputVerified(req, time.Now()) {
		return false
	}
	if !contentSensitive {
		return true
	}
	content := req.GetInputContent()
	if len(content) == 0 {
		content = []byte(req.GetLabels()["_content.prompt"])
	}
	if len(content) == 0 {
		return false
	}
	declared := req.GetInputSizeBytes()
	return declared == 0 || declared == int64(len(content))
}

func referencedInputVerified(req *pb.PolicyCheckRequest, now time.Time) bool {
	ref := req.GetInputRef()
	content := req.GetInputContent()
	if ref == nil || len(content) == 0 || len(ref.GetSha256()) != sha256.Size {
		return false
	}
	if ref.GetSizeBytes() != uint64(len(content)) || req.GetInputSizeBytes() != int64(len(content)) {
		return false
	}
	if strings.TrimSpace(ref.GetMediaType()) != strings.TrimSpace(req.GetInputContentType()) {
		return false
	}
	expires := ref.GetExpiresAt()
	if expires == nil || expires.CheckValid() != nil || !expires.AsTime().After(now) {
		return false
	}
	digest := sha256.Sum256(content)
	return subtle.ConstantTimeCompare(digest[:], ref.GetSha256()) == 1
}
