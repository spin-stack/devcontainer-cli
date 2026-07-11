package cli

import "testing"

func TestCompactTemplateMetadata_PreservesKeyOrder(t *testing.T) {
	input := `{
		"zeta": 1,
		"alpha": { "second": true, "first": false },
		"items": [ 1, 2 ]
	}`
	want := `{"zeta":1,"alpha":{"second":true,"first":false},"items":[1,2]}`

	got, err := compactTemplateMetadata(input)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("metadata = %s, want %s", got, want)
	}
}

func TestCompactTemplateMetadata_RejectsInvalidJSON(t *testing.T) {
	if _, err := compactTemplateMetadata(`{"broken":`); err == nil {
		t.Fatal("expected invalid metadata error")
	}
}
