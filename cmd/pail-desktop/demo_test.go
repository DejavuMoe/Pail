package main

import (
	"bytes"
	"image"
	"testing"
)

func TestDemoImagesMatchAndStayIsolated(t *testing.T) {
	first, err := newDemoEnv(t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(first.Close)
	second, err := newDemoEnv(t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(second.Close)
	names := []string{"hero.png", "logo-dark.png", "logo-light.png", "team-offsite.jpg", "product-shot-01.png", "product-shot-02.png", "banner-autumn.png", "og-card.png", "hero"}
	if len(demoImages()) != len(names) {
		t.Fatalf("got %d demo images, want %d", len(demoImages()), len(names))
	}
	for i, name := range names {
		key, hue := "assets/images/"+name, i
		if name == "hero" {
			key, hue = "website/img/hero.png", 9
		}
		want := demoImage(name, hue)
		for _, env := range []*demoEnv{first, second} {
			got, ok := env.Srv.Object("acme-assets", key)
			if !ok || !bytes.Equal(got, want) {
				t.Fatalf("%s: seeded bytes differ from the original generator", key)
			}
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(want))
		wantFormat := "png"
		if name == "team-offsite.jpg" {
			wantFormat = "jpeg"
		}
		if err != nil || cfg.Width != 320 || cfg.Height != 200 || format != wantFormat {
			t.Fatalf("%s: invalid image: %+v %s %v", key, cfg, format, err)
		}
		first.Srv.PutObject("acme-assets", key, []byte("changed"), "text/plain", nil)
		got, _ := second.Srv.Object("acme-assets", key)
		if !bytes.Equal(got, want) || !bytes.Equal(demoImages()[key], want) {
			t.Fatalf("%s: changing one server changed another server or the cached image", key)
		}
	}
}
