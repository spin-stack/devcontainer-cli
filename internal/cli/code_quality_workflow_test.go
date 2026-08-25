package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoCLIWorkflowDoesNotUploadDisabledCodeQualityCoverage(t *testing.T) {
	workflowPath := filepath.Join("..", "..", ".github", "workflows", "go-cli.yml")
	data, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatalf("read %s: %v", workflowPath, err)
	}

	workflow := string(data)
	for _, disabledSetting := range []string{"actions/upload-code-coverage", "code-quality: write"} {
		if strings.Contains(workflow, disabledSetting) {
			t.Errorf("%s still contains disabled Code Quality setting %q", workflowPath, disabledSetting)
		}
	}
}
