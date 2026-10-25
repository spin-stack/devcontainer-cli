package tsoracle

import "testing"

func TestDecodeEncode(t *testing.T) {
	f, err := Decode([]byte(`{"comment":"c","cases":[{"name":"n","fn":"f","args":["<&>",1],"result":{"b":1,"a":2}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	data, err := Encode(f)
	if err != nil {
		t.Fatal(err)
	}
	want := `{
  "comment": "c",
  "cases": [
    {
      "name": "n",
      "fn": "f",
      "args": [
        "<&>",
        1
      ],
      "result": {
        "b": 1,
        "a": 2
      }
    }
  ]
}
`
	if string(data) != want {
		t.Errorf("Encode =\n%s\nwant\n%s", data, want)
	}
	for _, bad := range []string{`{"cases":[{"args":[]}]}`, `{"cases":[],"x":1}`, `[`} {
		if _, err := Decode([]byte(bad)); err == nil {
			t.Errorf("Decode(%s) succeeded, want an error", bad)
		}
	}
}
