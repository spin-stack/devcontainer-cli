package docker

import (
	"testing"
)

func TestParseComposeVersion(t *testing.T) {
	tests := []struct {
		input string
		want  []int
	}{
		{"2.24.5", []int{2, 24, 5}},
		{"1.29.2", []int{1, 29, 2}},
		{"v2.17.0", []int{2, 17, 0}},
		{"Docker Compose version v2.24.5", []int{2, 24, 5}},
		{"docker-compose version 1.29.2, build 5becea4c", []int{1, 29, 2}},
		{"2.0", []int{2, 0}},
		{"invalid", nil},
	}
	for _, tt := range tests {
		got := parseComposeVersion(tt.input)
		if tt.want == nil && got != nil {
			t.Errorf("parseComposeVersion(%q) = %v, want nil", tt.input, got)
			continue
		}
		if tt.want != nil {
			if len(got) != len(tt.want) {
				t.Errorf("parseComposeVersion(%q) = %v, want %v", tt.input, got, tt.want)
				continue
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("parseComposeVersion(%q)[%d] = %d, want %d", tt.input, i, got[i], tt.want[i])
				}
			}
		}
	}
}

func TestToProjectName_New(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"my-project", "my-project"},
		{"My Project", "myproject"},
		{"My_Project", "my_project"},
		{"UPPER", "upper"},
		{"special!chars@here", "specialcharshere"},
	}
	for _, tt := range tests {
		got := ToProjectName(tt.input, true)
		if got != tt.want {
			t.Errorf("ToProjectName(%q, true) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestToProjectName_Old(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"my-project", "myproject"},
		{"My_Project", "myproject"},
		{"UPPER", "upper"},
	}
	for _, tt := range tests {
		got := ToProjectName(tt.input, false)
		if got != tt.want {
			t.Errorf("ToProjectName(%q, false) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSupportsAdditionalContexts(t *testing.T) {
	tests := []struct {
		version []int
		want    bool
	}{
		{[]int{2, 24, 5}, true},
		{[]int{2, 17, 0}, true},
		{[]int{2, 16, 0}, false},
		{[]int{1, 29, 2}, false},
		{[]int{3, 0, 0}, true},
		{nil, false},
	}
	for _, tt := range tests {
		c := &ComposeClient{Version: tt.version}
		got := c.SupportsAdditionalContexts()
		if got != tt.want {
			t.Errorf("SupportsAdditionalContexts(%v) = %v, want %v", tt.version, got, tt.want)
		}
	}
}

func TestUsesNewProjectNames(t *testing.T) {
	tests := []struct {
		version []int
		want    bool
	}{
		{[]int{2, 0, 0}, true},
		{[]int{1, 21, 0}, true},
		{[]int{1, 20, 0}, false},
		{[]int{1, 29, 2}, true},
		{nil, true}, // optimistic default
	}
	for _, tt := range tests {
		c := &ComposeClient{Version: tt.version}
		got := c.UsesNewProjectNames()
		if got != tt.want {
			t.Errorf("UsesNewProjectNames(%v) = %v, want %v", tt.version, got, tt.want)
		}
	}
}

func TestBuildGlobalArgs(t *testing.T) {
	c := &ComposeClient{}
	args := c.buildGlobalArgs([]string{"a.yml", "b.yml"}, ".env")
	expected := []string{"-f", "a.yml", "-f", "b.yml", "--env-file", ".env"}
	if len(args) != len(expected) {
		t.Fatalf("args = %v, want %v", args, expected)
	}
	for i, a := range args {
		if a != expected[i] {
			t.Errorf("args[%d] = %q, want %q", i, a, expected[i])
		}
	}
}

func TestBuildGlobalArgs_NoEnvFile(t *testing.T) {
	c := &ComposeClient{}
	args := c.buildGlobalArgs([]string{"compose.yml"}, "")
	if len(args) != 2 {
		t.Errorf("args = %v", args)
	}
}
