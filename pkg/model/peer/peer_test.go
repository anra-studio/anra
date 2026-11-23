package peer

import (
	"testing"

	pv "github.com/anra-studio/anra/gen/anra/peer/v1"
)

func TestAddressRequiresPublicHTTPOrigin(t *testing.T) {
	t.Parallel()

	for _, address := range []string{
		"127.0.0.1:8080",
		"http://0.0.0.0:8080",
		"http://127.0.0.1:0",
		"http://user@example.test",
		"https://example.test/api",
		"https://example.test?query=x",
		"https://example.test#fragment",
	} {
		t.Run(address, func(t *testing.T) {
			if got, err := Address(address); got != "" || err == nil {
				t.Fatalf("Address(%q) = %q, %v; want an error", address, got, err)
			}
		})
	}
	if got, err := Address("https://example.test/"); got != "https://example.test" || err != nil {
		t.Errorf("normalized origin = %q, %v", got, err)
	}
}

func TestNormalizeRejectsBlankSkillName(t *testing.T) {
	t.Parallel()

	value := &pv.Peer{
		Id:      "alice",
		Address: "http://127.0.0.1:8080",
		Skill: []*pv.Skill{
			{
				Id:   "research",
				Name: "Research",
			},
		},
	}
	if _, err := Normalize(value); err != nil {
		t.Fatal(err)
	}
	value.Skill[0].Name = "   "
	if got, err := Normalize(value); got != nil || err == nil {
		t.Fatalf("normalized invalid skill: %v, %v", got, err)
	}
}
