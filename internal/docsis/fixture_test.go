package docsis

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func downstreamFixture(t *testing.T) string {
	t.Helper()

	contents, err := os.ReadFile(filepath.Join("..", "hnap", "hnaptest", "testdata", "GetMotoStatusDownstreamChannelInfo.json"))
	if err != nil {
		t.Fatal(err)
	}

	var response struct {
		MotoConnDownstreamChannel string
	}
	if err := json.Unmarshal(contents, &response); err != nil {
		t.Fatal(err)
	}
	return response.MotoConnDownstreamChannel
}
