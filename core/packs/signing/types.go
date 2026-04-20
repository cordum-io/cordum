package signing

// ManifestVersion is the current canonical manifest version. Bumping
// this number is a signature-breaking change; old signatures remain
// verifiable because the verifier only accepts versions it recognises.
const ManifestVersion = 1

// SigningDomain is the domain-separation string mixed into every
// signed payload. Changing it invalidates all existing pack
// signatures.
//
// Sibling domains live in the delegation token service
// ("cordum.delegation.v1"), the MCP outbound signer, and the
// licensing service — they MUST remain distinct so a key compromise
// in one surface cannot be replayed in another.
const SigningDomain = "cordum.pack.v1"

// Algorithm advertised in envelopes. Only ed25519 is accepted today.
const AlgorithmEd25519 = "ed25519"

// FileKind categorises a signed file so operators can read a signed
// manifest and understand at a glance what each entry covers.
type FileKind string

const (
	FileKindManifest FileKind = "manifest"
	FileKindSchema   FileKind = "schema"
	FileKindWorkflow FileKind = "workflow"
	FileKindOverlay  FileKind = "overlay"
)

// FileEntry describes one hashed file inside a signed manifest.
// Paths are stored as forward-slash POSIX strings regardless of the
// host OS so a Windows-signed pack verifies on Linux (and vice
// versa).
type FileEntry struct {
	Path      string   `json:"path" yaml:"path"`
	SHA256    string   `json:"sha256" yaml:"sha256"`
	SizeBytes int64    `json:"size_bytes" yaml:"size_bytes"`
	Kind      FileKind `json:"kind" yaml:"kind"`
}

// Manifest is the canonical structure that gets signed. It contains
// every file that ships as part of the pack contract: pack.yaml plus
// every file referenced from pack.yaml's resources/overlays blocks.
//
// The Files slice is ALWAYS sorted by Path ascending before signing
// so two independent walks of the same pack yield byte-identical
// signing bytes.
type Manifest struct {
	Version     int         `json:"version" yaml:"version"`
	PackID      string      `json:"pack_id" yaml:"pack_id"`
	PackVersion string      `json:"pack_version" yaml:"pack_version"`
	SignedAt    string      `json:"signed_at" yaml:"signed_at"`
	Algorithm   string      `json:"algorithm" yaml:"algorithm"`
	Files       []FileEntry `json:"files" yaml:"files"`
}

// Signature holds the Ed25519 signature bytes plus the key id used to
// sign and the domain string so a verifier can reject a payload that
// was signed under a different domain.
type Signature struct {
	KeyID     string `json:"key_id" yaml:"key_id"`
	Algorithm string `json:"algorithm" yaml:"algorithm"`
	Value     string `json:"value" yaml:"value"`
	Domain    string `json:"domain" yaml:"domain"`
}

// SignedManifest is the on-disk envelope written to pack.yaml.sig.
// It wraps the canonical Manifest together with the signature
// metadata. The two on-disk formats (YAML + JSON) deserialise to this
// same struct.
type SignedManifest struct {
	APIVersion string    `json:"apiVersion" yaml:"apiVersion"`
	Kind       string    `json:"kind" yaml:"kind"`
	Metadata   Metadata  `json:"metadata" yaml:"metadata"`
	Signature  Signature `json:"signature" yaml:"signature"`
	Manifest   Manifest  `json:"manifest" yaml:"manifest"`
}

// Metadata carries the identifiers an operator can diff without
// parsing the manifest body.
type Metadata struct {
	PackID      string `json:"pack_id" yaml:"pack_id"`
	PackVersion string `json:"pack_version" yaml:"pack_version"`
	SignedAt    string `json:"signed_at" yaml:"signed_at"`
}

const (
	// EnvelopeAPIVersion is the apiVersion string written to every
	// envelope. A future v2 envelope would choose a new string here
	// so both envelopes can coexist in the wild during a migration.
	EnvelopeAPIVersion = "cordum.io/v1alpha1"
	// EnvelopeKind identifies the envelope as a pack-signature
	// record.
	EnvelopeKind = "PackSignature"
)
