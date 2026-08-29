package clashsub

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

// This integration test is skipped in normal test runs. It is used locally to
// validate a user-supplied subscription without committing its private URL.
func TestLiveV2RayNSubscription(t *testing.T) {
	rawURL := os.Getenv("EASY_NET_TEST_SUBSCRIPTION_URL")
	mihomo := os.Getenv("EASY_NET_TEST_MIHOMO")
	if rawURL == "" || mihomo == "" {
		t.Skip("live subscription test is not configured")
	}
	root := t.TempDir()
	t.Setenv("EASY_NET_MIHOMO", mihomo)
	manager, err := New(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	subscription, err := manager.Import("live-v2rayn", rawURL, 19999, 0, false, false)
	if err != nil {
		t.Fatal(err)
	}
	nodes := subscription.Nodes
	typeCounts := map[string]int{}
	for index, node := range nodes {
		typeCounts[node.Type]++
		workDir := filepath.Join(root, "syntax", node.Type, strconv.Itoa(index+1))
		configPath := filepath.Join(workDir, "config.yaml")
		if err := WriteMihomoConfig(configPath, 20000+index, node.Raw, false, false); err != nil {
			t.Fatalf("node %d (%s): %v", index+1, node.Type, err)
		}
		command := exec.Command(mihomo, "-t", "-d", workDir, "-f", configPath)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("mihomo rejected node %d (%s): %v\n%s", index+1, node.Type, err, output)
		}
	}
	t.Logf("parsed and validated %d nodes by type: %v", len(nodes), typeCounts)

	attempts := 0
	for _, node := range nodes {
		if attempts >= 10 {
			break
		}
		attempts++
		if err := manager.withTempNodeSOCKS("live", node, probeNodeThroughSOCKS); err == nil {
			t.Logf("live SOCKS5 probe succeeded with node %d (%s)", attempts, node.Type)
			return
		} else {
			t.Logf("live SOCKS5 probe failed with node %d (%s): %v", attempts, node.Type, err)
		}
	}
	t.Fatal("the first ten subscription nodes all failed the live SOCKS5 probe")
}
