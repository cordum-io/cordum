package safetykernel

import (
	"testing"

	"github.com/cordum/cordum/core/infra/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func scopeConfig() *config.ScopeConfig {
	return &config.ScopeConfig{
		InstructionPath: "instruction",
		ItemsPath:       "items",
		CategoryPath:    "category",
		NamePath:        "name",
		AllowedCategories: map[string][]string{
			"grocery": {"produce", "dairy", "meat", "bakery", "beverages", "snacks", "frozen", "pantry", "household", "personal_care"},
		},
		Aliases: map[string]string{
			"gift-card": "gift_card",
			"giftcard":  "gift_card",
			"gift card": "gift_card",
		},
		OnMissingInput: "deny",
		OnAmbiguous:    "deny",
	}
}

func TestScopeEvaluator_AllowsValidGroceryCart(t *testing.T) {
	cfg := scopeConfig()
	content := []byte(`{
		"instruction": "Buy grocery items for dinner",
		"items": [
			{"name": "Milk", "category": "dairy", "price": 12},
			{"name": "Bread", "category": "bakery", "price": 8}
		]
	}`)
	violated, findings := evaluateScope(cfg, content)
	assert.False(t, violated, "valid grocery cart should not be denied")
	assert.Empty(t, findings)
}

func TestScopeEvaluator_DeniesGiftCardInGroceryCart(t *testing.T) {
	cfg := scopeConfig()
	content := []byte(`{
		"instruction": "Buy grocery items for dinner",
		"items": [
			{"name": "Milk", "category": "dairy", "price": 12},
			{"name": "Amazon Gift Card", "category": "gift_card", "price": 500}
		]
	}`)
	violated, findings := evaluateScope(cfg, content)
	assert.True(t, violated, "gift_card in grocery cart should be denied")
	require.Len(t, findings, 1)
	assert.Equal(t, "scope_violation", findings[0].Type)
	assert.Contains(t, findings[0].Detail, "gift_card")
	assert.Contains(t, findings[0].Detail, "grocery")
	assert.Equal(t, "Amazon Gift Card", findings[0].Item)
}

func TestScopeEvaluator_AliasNormalization(t *testing.T) {
	cfg := scopeConfig()
	content := []byte(`{
		"instruction": "Buy grocery items",
		"items": [
			{"name": "Steam Card", "category": "Gift-Card", "price": 100}
		]
	}`)
	violated, findings := evaluateScope(cfg, content)
	assert.True(t, violated, "Gift-Card (alias) should normalize to gift_card and be denied")
	require.Len(t, findings, 1)
	assert.Equal(t, "scope_violation", findings[0].Type)
}

func TestScopeEvaluator_MixedCart_ReportsOnlyViolations(t *testing.T) {
	cfg := scopeConfig()
	content := []byte(`{
		"instruction": "Buy grocery items",
		"items": [
			{"name": "Apples", "category": "produce", "price": 15},
			{"name": "Bitcoin", "category": "cryptocurrency", "price": 50000},
			{"name": "Cheese", "category": "dairy", "price": 20},
			{"name": "Ammo", "category": "weapons", "price": 100}
		]
	}`)
	violated, findings := evaluateScope(cfg, content)
	assert.True(t, violated)
	assert.Len(t, findings, 2, "should report 2 violations (cryptocurrency, weapons)")
}

func TestScopeEvaluator_MissingInstruction_Denies(t *testing.T) {
	cfg := scopeConfig()
	content := []byte(`{"items": [{"name": "Milk", "category": "dairy"}]}`)
	violated, findings := evaluateScope(cfg, content)
	assert.True(t, violated)
	require.Len(t, findings, 1)
	assert.Equal(t, "missing_input", findings[0].Type)
}

func TestScopeEvaluator_MissingInstruction_AllowsWhenConfigured(t *testing.T) {
	cfg := scopeConfig()
	cfg.OnMissingInput = "allow"
	content := []byte(`{"items": [{"name": "Milk", "category": "dairy"}]}`)
	violated, _ := evaluateScope(cfg, content)
	assert.False(t, violated)
}

func TestScopeEvaluator_MissingItems_Denies(t *testing.T) {
	cfg := scopeConfig()
	content := []byte(`{"instruction": "Buy groceries"}`)
	violated, findings := evaluateScope(cfg, content)
	assert.True(t, violated)
	require.Len(t, findings, 1)
	assert.Equal(t, "missing_input", findings[0].Type)
}

func TestScopeEvaluator_MalformedJSON_Denies(t *testing.T) {
	cfg := scopeConfig()
	content := []byte(`not json at all`)
	violated, findings := evaluateScope(cfg, content)
	assert.True(t, violated)
	require.Len(t, findings, 1)
	assert.Equal(t, "malformed_payload", findings[0].Type)
}

func TestScopeEvaluator_AmbiguousIntent_Denies(t *testing.T) {
	cfg := scopeConfig()
	content := []byte(`{
		"instruction": "Do something vague",
		"items": [{"name": "Milk", "category": "dairy"}]
	}`)
	violated, findings := evaluateScope(cfg, content)
	assert.True(t, violated)
	require.Len(t, findings, 1)
	assert.Equal(t, "ambiguous_intent", findings[0].Type)
}

func TestScopeEvaluator_AmbiguousIntent_AllowsWhenConfigured(t *testing.T) {
	cfg := scopeConfig()
	cfg.OnAmbiguous = "allow"
	content := []byte(`{
		"instruction": "Do something vague",
		"items": [{"name": "Milk", "category": "dairy"}]
	}`)
	violated, _ := evaluateScope(cfg, content)
	assert.False(t, violated)
}

func TestScopeEvaluator_EmptyAllowedCategories_AllowsAll(t *testing.T) {
	cfg := scopeConfig()
	cfg.AllowedCategories["grocery"] = nil // empty = all allowed
	content := []byte(`{
		"instruction": "Buy grocery items",
		"items": [
			{"name": "Gift Card", "category": "gift_card", "price": 500}
		]
	}`)
	violated, _ := evaluateScope(cfg, content)
	assert.False(t, violated, "empty allowed categories = all categories permitted")
}

func TestScopeEvaluator_NilConfig_NoOp(t *testing.T) {
	violated, findings := evaluateScope(nil, []byte(`{}`))
	assert.False(t, violated)
	assert.Nil(t, findings)
}

func TestValidateScopeConfig_RequiresInstructionPath(t *testing.T) {
	cfg := &config.ScopeConfig{ItemsPath: "items"}
	err := validateScopeConfig(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "instruction_path")
}

func TestValidateScopeConfig_RequiresItemsPath(t *testing.T) {
	cfg := &config.ScopeConfig{InstructionPath: "instruction"}
	err := validateScopeConfig(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "items_path")
}

func TestValidateScopeConfig_ValidConfig(t *testing.T) {
	err := validateScopeConfig(scopeConfig())
	assert.NoError(t, err)
}

func TestNormalizeCategory(t *testing.T) {
	aliases := map[string]string{"gift-card": "gift_card", "giftcard": "gift_card"}
	assert.Equal(t, "gift_card", normalizeCategory("Gift-Card", aliases))
	assert.Equal(t, "gift_card", normalizeCategory("giftcard", aliases))
	assert.Equal(t, "produce", normalizeCategory("Produce", aliases))
	assert.Equal(t, "dairy", normalizeCategory("  DAIRY  ", aliases))
}

func TestClassifyIntent(t *testing.T) {
	categories := map[string][]string{
		"grocery":     {"produce", "dairy"},
		"electronics": {"phones", "computers"},
	}
	assert.Equal(t, "grocery", classifyIntent("Buy grocery items for dinner", categories))
	assert.Equal(t, "electronics", classifyIntent("Purchase electronics for office", categories))
	assert.Equal(t, "", classifyIntent("Do something vague", categories))
}
