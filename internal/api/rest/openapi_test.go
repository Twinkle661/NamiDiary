package rest_test

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/Twinkle661/NamiDiary/internal/api/rest"
)

func TestOpenAPIRoutes(t *testing.T) {
	b, err := os.ReadFile("../../../openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		OpenAPI string                    `yaml:"openapi"`
		Paths   map[string]map[string]any `yaml:"paths"`
	}
	if err = yaml.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.OpenAPI != "3.1.0" {
		t.Fatal("wrong version")
	}
	count := 0
	for _, methods := range doc.Paths {
		count += len(methods)
	}
	if count != len(rest.Routes) {
		t.Fatalf("route count %d != %d", count, len(rest.Routes))
	}
	for _, route := range rest.Routes {
		method, path, _ := strings.Cut(route, " ")
		if _, ok := doc.Paths[path][strings.ToLower(method)]; !ok {
			t.Errorf("missing %s", route)
		}
	}
}
