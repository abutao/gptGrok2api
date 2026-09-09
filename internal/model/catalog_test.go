package model

import "testing"

func TestCatalogContainsCoreModels(t *testing.T) {
	items := Catalog()
	for _, id := range []string{"grok-4.20-fast", "grok-imagine-image", "grok-imagine-video"} {
		if _, ok := Find(items, id); !ok {
			t.Fatalf("catalog missing %q", id)
		}
	}
}

func TestImageModelChatCompatibilityRoute(t *testing.T) {
	for _, id := range []string{
		"gpt-image-2",
		"gpt-image-2.5",
		"gpt-image-2.5-flare",
		"gpt-image-2.5-sunburst",
	} {
		item, found := Find(Catalog(), id)
		if !found || item.Capability&Image == 0 {
			t.Fatalf("%s must be an image model", id)
		}
		route, ok := ResolveChat(id)
		if !ok || !route.OpenAI || !route.Image {
			t.Fatalf("%s must use the OpenAI image chat-completions route", id)
		}
		if item.Capability&Chat != 0 {
			t.Fatalf("%s must remain hidden from the normal chat catalog", id)
		}
	}
}
