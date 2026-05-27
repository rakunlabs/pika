package service_test

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/rakunlabs/pika/internal/service"
)

func TestGoTemplateDisabledByDefault(t *testing.T) {
	svc := newInheritTestService(t)

	if _, err := svc.SetFile(t.Context(), "app/raw", &service.File{
		Meta: service.FileMeta{Format: "raw"},
		Data: []byte(`name={{ upper "prod" }}`),
	}, nil, ""); err != nil {
		t.Fatalf("SetFile: %v", err)
	}

	got, err := svc.GetData(t.Context(), "app/raw", "0", "")
	if err != nil {
		t.Fatalf("GetData: %v", err)
	}
	if string(got.Data) != `name={{ upper "prod" }}` {
		t.Fatalf("template rendered while disabled: %q", got.Data)
	}
}

func TestGoTemplateGetDataRendersMugoFunctions(t *testing.T) {
	svc := newInheritTestService(t)

	if _, err := svc.SetFile(t.Context(), "app/template", &service.File{
		Meta: service.FileMeta{Format: "raw", GoTemplate: true},
		Data: []byte(`name={{ upper "prod" }} path={{ .Path }}`),
	}, nil, ""); err != nil {
		t.Fatalf("SetFile: %v", err)
	}

	got, err := svc.GetData(t.Context(), "app/template", "0", "")
	if err != nil {
		t.Fatalf("GetData: %v", err)
	}
	if got.Error != "" {
		t.Fatalf("GetData error: %s", got.Error)
	}
	if string(got.Data) != "name=PROD path=app/template" {
		t.Fatalf("rendered data = %q", got.Data)
	}
}

func TestGoTemplateRenderFileRunsBeforeFormatParsing(t *testing.T) {
	svc := newInheritTestService(t)

	got, err := svc.RenderFile(t.Context(), "app/config.yaml", "", `name: {{ upper "prod" }}`, &service.FileMeta{
		Format:     "yaml",
		GoTemplate: true,
	})
	if err != nil {
		t.Fatalf("RenderFile: %v", err)
	}
	if got.Error != "" {
		t.Fatalf("RenderFile error: %s", got.Error)
	}
	decoded, err := base64.StdEncoding.DecodeString(got.Data)
	if err != nil {
		t.Fatalf("decode render result: %v", err)
	}
	if !strings.Contains(string(decoded), "PROD") {
		t.Fatalf("template output missing from rendered yaml: %q", decoded)
	}
}

func TestGoTemplateInternalInheritedSourceRendersBeforeMerge(t *testing.T) {
	svc := newInheritTestService(t)

	if _, err := svc.SetFile(t.Context(), "base", &service.File{
		Meta: service.FileMeta{Format: "json", GoTemplate: true},
		Data: []byte(`{"name":"{{ upper "prod" }}"}`),
	}, nil, ""); err != nil {
		t.Fatalf("SetFile(base): %v", err)
	}
	if _, err := svc.SetFile(t.Context(), "child", &service.File{
		Meta: service.FileMeta{Format: "json", Inherits: []service.InheritEntry{{Source: "base"}}},
		Data: []byte(`{"local":true}`),
	}, nil, ""); err != nil {
		t.Fatalf("SetFile(child): %v", err)
	}

	got, err := svc.GetData(t.Context(), "child", "0", "")
	if err != nil {
		t.Fatalf("GetData: %v", err)
	}
	if !strings.Contains(string(got.Data), `"name":"PROD"`) {
		t.Fatalf("inherited template was not rendered before merge: %s", got.Data)
	}
}

func TestGoTemplateRejectsUnsafeFunctions(t *testing.T) {
	svc := newInheritTestService(t)

	got, err := svc.RenderFile(t.Context(), "unsafe", "", `{{ os.ReadFile "/etc/passwd" }}`, &service.FileMeta{
		Format:     "raw",
		GoTemplate: true,
	})
	if err != nil {
		t.Fatalf("RenderFile: %v", err)
	}
	if got.Error == "" || !strings.Contains(got.Error, "os") {
		t.Fatalf("expected unsafe os function rejection, got %q", got.Error)
	}
}
