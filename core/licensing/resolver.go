package licensing

import (
	"crypto/ed25519"
	"log/slog"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
)

const maxInt64AsUint64 = uint64(1<<63 - 1)

type EntitlementResolver struct {
	loadFromEnv      func() (*License, error)
	publicKeyFromEnv func() (ed25519.PublicKey, error)
	verify           func(*License, ed25519.PublicKey, time.Time) error
	now              func() time.Time
	state            atomic.Value
}

type resolverSnapshot struct {
	Plan         Plan
	Entitlements Entitlements
	Info         LicenseInfo
	Rights       *Rights
}

func NewEntitlementResolver() *EntitlementResolver {
	resolver := &EntitlementResolver{
		loadFromEnv:      LoadFromEnv,
		publicKeyFromEnv: PublicKeyFromEnv,
		now: func() time.Time {
			return time.Now().UTC()
		},
		verify: func(license *License, publicKey ed25519.PublicKey, now time.Time) error {
			if license == nil {
				return nil
			}
			return license.Verify(publicKey, now)
		},
	}
	resolver.state.Store(buildResolverSnapshot(PlanCommunity, DefaultEntitlements(PlanCommunity), nil, "active"))
	return resolver
}

func (r *EntitlementResolver) Init() {
	if r == nil {
		return
	}

	license, err := r.loader()()
	if err != nil {
		r.storeCommunityFallback("fallback", "licensing: failed to load license; falling back to community", err)
		return
	}
	if license == nil {
		r.state.Store(buildResolverSnapshot(PlanCommunity, DefaultEntitlements(PlanCommunity), nil, "active"))
		return
	}

	publicKey, err := r.keyLoader()()
	if err != nil {
		r.storeCommunityFallback("fallback", "licensing: failed to load public key; falling back to community", err)
		return
	}
	if err := r.verifier()(license, publicKey, r.clock()()); err != nil {
		r.storeCommunityFallback("fallback", "licensing: invalid license; falling back to community", err)
		return
	}

	plan := ParsePlan(licenseClaimString(license, "Tier", "Plan"))
	entitlements := mergeEntitlements(DefaultEntitlements(plan), licenseClaimEntitlements(license))
	r.state.Store(buildResolverSnapshot(plan, entitlements, license, licenseStatus(license)))
}

// Reload re-reads the license from env/file, re-validates, and atomically
// updates the cached snapshot. Safe to call concurrently from any goroutine.
// Returns the new resolved plan and any load/verify error (community fallback
// is applied on error, so the resolver always remains usable).
func (r *EntitlementResolver) Reload() (Plan, error) {
	if r == nil {
		return PlanCommunity, nil
	}
	r.Init()
	return r.ResolvedPlan(), nil
}

func (r *EntitlementResolver) ResolvedPlan() Plan {
	return r.snapshot().Plan
}

func (r *EntitlementResolver) Entitlements() Entitlements {
	return r.snapshot().Entitlements
}

func (r *EntitlementResolver) LicenseInfo() *LicenseInfo {
	info := r.snapshot().Info
	return &info
}

func (r *EntitlementResolver) Rights() *Rights {
	rights := r.snapshot().Rights
	if rights == nil {
		return nil
	}
	cloned := *rights
	return &cloned
}

func (r *EntitlementResolver) snapshot() resolverSnapshot {
	if r == nil {
		return buildResolverSnapshot(PlanCommunity, DefaultEntitlements(PlanCommunity), nil, "active")
	}
	if current, ok := r.state.Load().(resolverSnapshot); ok {
		return current
	}
	return buildResolverSnapshot(PlanCommunity, DefaultEntitlements(PlanCommunity), nil, "active")
}

func (r *EntitlementResolver) storeCommunityFallback(status, message string, err error) {
	if err != nil {
		slog.Warn(message, "error", err)
	}
	r.state.Store(buildResolverSnapshot(PlanCommunity, DefaultEntitlements(PlanCommunity), nil, status))
}

func (r *EntitlementResolver) loader() func() (*License, error) {
	if r != nil && r.loadFromEnv != nil {
		return r.loadFromEnv
	}
	return LoadFromEnv
}

func (r *EntitlementResolver) keyLoader() func() (ed25519.PublicKey, error) {
	if r != nil && r.publicKeyFromEnv != nil {
		return r.publicKeyFromEnv
	}
	return PublicKeyFromEnv
}

func (r *EntitlementResolver) verifier() func(*License, ed25519.PublicKey, time.Time) error {
	if r != nil && r.verify != nil {
		return r.verify
	}
	return func(license *License, publicKey ed25519.PublicKey, now time.Time) error {
		if license == nil {
			return nil
		}
		return license.Verify(publicKey, now)
	}
}

func (r *EntitlementResolver) clock() func() time.Time {
	if r != nil && r.now != nil {
		return r.now
	}
	return func() time.Time {
		return time.Now().UTC()
	}
}

// ForceState is a test helper that bypasses env/file loading and stores a
// synthetic resolver snapshot directly.
func (r *EntitlementResolver) ForceState(plan Plan, entitlements Entitlements, rights *Rights) {
	r.ForceStateWithStatus(plan, entitlements, rights, "active")
}

// ForceStateWithStatus is the same as ForceState, but allows tests to set an
// explicit runtime status such as grace, degraded, or invalid.
func (r *EntitlementResolver) ForceStateWithStatus(plan Plan, entitlements Entitlements, rights *Rights, status string) {
	if r == nil {
		return
	}

	var license *License
	if rights != nil {
		cloned := *rights
		license = &License{Payload: Claims{Rights: &cloned}}
	}

	status = strings.TrimSpace(status)
	if status == "" {
		status = "active"
	}

	r.state.Store(buildResolverSnapshot(plan, entitlements, license, status))
}

func buildResolverSnapshot(plan Plan, entitlements Entitlements, license *License, status string) resolverSnapshot {
	plan = plan.Normalized()
	info := LicenseInfo{
		Mode:           string(plan),
		Status:         strings.TrimSpace(status),
		Plan:           plan.DisplayName(),
		OrgID:          licenseClaimString(license, "OrgID"),
		LicenseID:      licenseClaimString(license, "LicenseID"),
		DeploymentType: licenseClaimString(license, "DeploymentType"),
		IssuedAt:       licenseClaimString(license, "IssuedAt"),
		NotBefore:      licenseClaimString(license, "NotBefore"),
		ExpiresAt:      licenseClaimString(license, "ExpiresAt"),
	}
	if features := enabledFeatureNames(entitlements); len(features) > 0 {
		info.Features = features
	}
	if limits := entitlementLimitMap(entitlements); len(limits) > 0 {
		info.Limits = limits
	}
	var rights *Rights
	if license != nil && license.Payload.Rights != nil {
		cloned := *license.Payload.Rights
		rights = &cloned
	}
	return resolverSnapshot{
		Plan:         plan,
		Entitlements: entitlements,
		Info:         info,
		Rights:       rights,
	}
}

func licenseStatus(license *License) string {
	if license == nil {
		return "active"
	}
	switch license.ExpiryState {
	case ExpiryStateWarning:
		return "warning"
	case ExpiryStateGrace:
		return "grace"
	case ExpiryStateDegraded:
		return "degraded"
	default:
		return "active"
	}
}

func mergeEntitlements(base, override Entitlements) Entitlements {
	merged := base
	mergeValue(reflect.ValueOf(&merged).Elem(), reflect.ValueOf(override), "")
	// Presence-aware override: a signature-verified claim that EXPLICITLY set a
	// scalar limit is authoritative over the tier default (it may RESTRICT below
	// it — security-safe; the generic mergeValue above MAX-merges, which
	// under-enforces a lower claim limit against a BUG-016 Unlimited default).
	// Fields the claim did not set keep the merged tier default.
	applyPresentLimits(&merged, override)
	return merged
}

func mergeValue(dst, src reflect.Value, fieldName string) {
	dst = indirectValue(dst)
	src = indirectValue(src)
	if !dst.IsValid() || !src.IsValid() || !dst.CanSet() {
		return
	}
	if dst.Type() != src.Type() {
		return
	}

	switch dst.Kind() {
	case reflect.Struct:
		for i := range dst.NumField() {
			field := dst.Type().Field(i)
			if field.PkgPath != "" {
				continue
			}
			mergeValue(dst.Field(i), src.Field(i), field.Name)
		}
	case reflect.Bool:
		if src.Bool() {
			dst.SetBool(true)
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		dst.SetInt(mergeLimitValues(dst.Int(), src.Int()))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		if src.Uint() > dst.Uint() {
			dst.SetUint(src.Uint())
		}
	case reflect.String:
		next := strings.TrimSpace(src.String())
		if next == "" {
			return
		}
		current := strings.TrimSpace(dst.String())
		if isApprovalModeField(fieldName, dst.Type()) {
			if approvalModeRank(next) > approvalModeRank(current) {
				dst.SetString(next)
			}
			return
		}
		if current == "" {
			dst.SetString(next)
		}
	case reflect.Pointer:
		if src.IsNil() {
			return
		}
		if dst.IsNil() {
			dst.Set(src)
			return
		}
		mergeValue(dst.Elem(), src.Elem(), fieldName)
	case reflect.Slice, reflect.Map:
		if dst.IsNil() && !src.IsNil() {
			dst.Set(src)
		}
	}
}

func mergeLimitValues(current, next int64) int64 {
	if current == Unlimited || next == Unlimited {
		return Unlimited
	}
	if next > current {
		return next
	}
	return current
}

// applyPresentLimits overrides scalar int64 limit fields that the (verified)
// claim EXPLICITLY set, making them authoritative over the tier default. This
// fixes the MAX-merge under-enforcement (BUG-016 sibling): a claim limit lower
// than the tier default is now honored (including explicit 0=deny and explicit
// Unlimited). Fields the claim did not set keep the merged tier default. No-op
// when the override carries no presence info (an in-code override), preserving
// the legacy merge for non-JSON callers.
func applyPresentLimits(merged *Entitlements, override Entitlements) {
	if override.present == nil {
		return
	}
	mv := reflect.ValueOf(merged).Elem()
	ov := reflect.ValueOf(override)
	t := mv.Type()
	for i := range t.NumField() {
		f := t.Field(i)
		if f.PkgPath != "" || mv.Field(i).Kind() != reflect.Int64 {
			continue
		}
		key := entitlementJSONKey(f.Tag.Get("json"))
		if key != "" && override.wasSet(key) {
			mv.Field(i).SetInt(ov.Field(i).Int())
		}
	}
}

// entitlementJSONKey returns the field-name part of a struct json tag
// ("name,omitempty" -> "name").
func entitlementJSONKey(tag string) string {
	if idx := strings.IndexByte(tag, ','); idx >= 0 {
		return tag[:idx]
	}
	return tag
}

func isApprovalModeField(fieldName string, typ reflect.Type) bool {
	if strings.EqualFold(strings.TrimSpace(fieldName), "ApprovalMode") {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(typ.Name()), "ApprovalMode")
}

func approvalModeRank(raw string) int {
	switch ParseApprovalMode(raw) {
	case ApprovalModeCustom:
		return 3
	case ApprovalModeMulti:
		return 2
	default:
		return 1
	}
}

func licenseClaimEntitlements(license *License) Entitlements {
	var entitlements Entitlements
	payload := licensePayloadValue(license)
	if !payload.IsValid() || payload.Kind() != reflect.Struct {
		return entitlements
	}
	field := payload.FieldByName("Entitlements")
	field = indirectValue(field)
	if !field.IsValid() {
		return entitlements
	}
	if resolved, ok := field.Interface().(Entitlements); ok {
		return resolved
	}
	return entitlements
}

func licenseClaimString(license *License, names ...string) string {
	payload := licensePayloadValue(license)
	if !payload.IsValid() || payload.Kind() != reflect.Struct {
		return ""
	}
	for _, name := range names {
		field := payload.FieldByName(name)
		if raw := stringFieldValue(field); raw != "" {
			return raw
		}
	}
	return ""
}

func licensePayloadValue(license *License) reflect.Value {
	root := indirectValue(reflect.ValueOf(license))
	if !root.IsValid() || root.Kind() != reflect.Struct {
		return reflect.Value{}
	}
	for _, name := range []string{"Payload", "Claims"} {
		field := indirectValue(root.FieldByName(name))
		if field.IsValid() {
			return field
		}
	}
	return root
}

func stringFieldValue(value reflect.Value) string {
	value = indirectValue(value)
	if !value.IsValid() {
		return ""
	}
	switch value.Kind() {
	case reflect.String:
		return strings.TrimSpace(value.String())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(value.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return strconv.FormatUint(value.Uint(), 10)
	case reflect.Struct:
		if tm, ok := value.Interface().(time.Time); ok && !tm.IsZero() {
			return tm.UTC().Format(time.RFC3339)
		}
	}
	return ""
}

func enabledFeatureNames(entitlements Entitlements) []string {
	features := collectFeatureNames(reflect.ValueOf(entitlements), nil)
	if len(features) == 0 {
		return nil
	}
	items := make([]string, 0, len(features))
	for feature := range features {
		items = append(items, feature)
	}
	sort.Strings(items)
	return items
}

func collectFeatureNames(value reflect.Value, features map[string]struct{}) map[string]struct{} {
	value = indirectValue(value)
	if !value.IsValid() {
		return features
	}
	if features == nil {
		features = make(map[string]struct{})
	}
	if value.Kind() != reflect.Struct {
		return features
	}
	for i := range value.NumField() {
		field := value.Type().Field(i)
		if field.PkgPath != "" {
			continue
		}
		current := indirectValue(value.Field(i))
		if !current.IsValid() {
			continue
		}
		switch current.Kind() {
		case reflect.Bool:
			if current.Bool() {
				features[toSnakeCase(field.Name)] = struct{}{}
			}
		case reflect.Struct:
			collectFeatureNames(current, features)
		}
	}
	return features
}

func entitlementLimitMap(entitlements Entitlements) map[string]int64 {
	limits := make(map[string]int64)
	collectLimitValues(reflect.ValueOf(entitlements), limits)
	if len(limits) == 0 {
		return nil
	}
	return limits
}

func collectLimitValues(value reflect.Value, limits map[string]int64) {
	value = indirectValue(value)
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return
	}
	for i := range value.NumField() {
		field := value.Type().Field(i)
		if field.PkgPath != "" {
			continue
		}
		current := indirectValue(value.Field(i))
		if !current.IsValid() {
			continue
		}
		switch current.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			value := current.Int()
			if value != 0 {
				limits[toSnakeCase(field.Name)] = value
			}
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			if value := current.Uint(); value != 0 {
				if value > maxInt64AsUint64 {
					limits[toSnakeCase(field.Name)] = int64(maxInt64AsUint64) // #nosec G115 -- conversion is MaxInt64.
				} else {
					limits[toSnakeCase(field.Name)] = int64(value) // #nosec G115 -- value is bounded by MaxInt64 above.
				}
			}
		case reflect.Struct:
			collectLimitValues(current, limits)
		}
	}
}

func setNamedIntField(target any, value int64, names ...string) {
	for _, name := range names {
		field := namedField(target, name)
		if !field.IsValid() || !field.CanSet() {
			continue
		}
		switch field.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			field.SetInt(value)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			if value >= 0 {
				field.SetUint(uint64(value))
			}
		}
	}
}

func setNamedBoolField(target any, value bool, names ...string) {
	for _, name := range names {
		field := namedField(target, name)
		if field.IsValid() && field.CanSet() && field.Kind() == reflect.Bool {
			field.SetBool(value)
		}
	}
}

func setNamedStringField(target any, value string, names ...string) {
	for _, name := range names {
		field := namedField(target, name)
		if field.IsValid() && field.CanSet() && field.Kind() == reflect.String {
			field.SetString(strings.TrimSpace(value))
		}
	}
}

func namedField(target any, name string) reflect.Value {
	value := indirectValue(reflect.ValueOf(target))
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return reflect.Value{}
	}
	return value.FieldByName(name)
}

func indirectValue(value reflect.Value) reflect.Value {
	for value.IsValid() && (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) {
		if value.IsNil() {
			return reflect.Value{}
		}
		value = value.Elem()
	}
	return value
}

func toSnakeCase(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	runes := []rune(raw)
	var out []rune
	for i, r := range runes {
		if unicode.IsUpper(r) {
			if i > 0 {
				prev := runes[i-1]
				nextIsLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
				if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextIsLower) {
					out = append(out, '_')
				}
			}
			out = append(out, unicode.ToLower(r))
			continue
		}
		out = append(out, r)
	}
	return string(out)
}
