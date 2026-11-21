package e2e_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/cucumber/godog"
)

func initializeConfig(sc *godog.ScenarioContext, c *cliScenario, team *teamScenario) {
	sc.Step(`^a lobby TOML configuration based on the repository example$`, func() error {
		data, err := os.ReadFile("../../anra.example.toml")
		if err != nil {
			return err
		}
		text := strings.Replace(string(data), `http://127.0.0.1:9090`, team.registry.peer, 1)
		text = strings.ReplaceAll(text, `data-dir = ".anra"`, `data-dir = "cli"`)
		text = strings.ReplaceAll(text, `endpoint = "127.0.0.1:8080"`, `endpoint = "127.0.0.1:0"`)
		return os.WriteFile(c.configPath(), []byte(text), 0o600)
	})
	sc.Step(`^I ask configured agent "([^"]*)" for its role$`, func(id string) error {
		c.execute("ask", "--config", c.configPath(), "--agent", id, "report your configured role")
		return c.err
	})
	sc.Step(`^a TOML CLI configuration with Alice and Bob$`, func() error { return c.writeAgentConfig("") })
	sc.Step(`^I run the configured CLI agent "([^"]*)" with "([^"]*)"$`, func(id, text string) error {
		c.execute("run", "--config", c.configPath(), "--agent", id, text)
		return c.err
	})
	sc.Step(`^the configured CLI agent "([^"]*)" is serving$`, func(id string) error {
		p := &cliScenario{s: c.s}
		team.agents[id] = p
		return p.startProcess(id, "--config", c.configPath(), "serve", "--agent", id)
	})
	sc.Step(`^I change the role of configured agent "([^"]*)" to "([^"]*)"$`, func(id, system string) error {
		data, err := os.ReadFile(c.configPath())
		if err != nil {
			return err
		}
		pattern := regexp.MustCompile(`(?s)(\[agents\.` + regexp.QuoteMeta(id) + `\][^\[]*?)role = [^\n]*`)
		if !pattern.Match(data) {
			return fmt.Errorf("configuration has no role setting for agent %q", id)
		}
		text := pattern.ReplaceAllString(string(data), "${1}role = "+strconv.Quote(system))
		return os.WriteFile(c.configPath(), []byte(text), 0o600)
	})
	sc.Step(`^the environment tries to override every agent setting$`, func() {
		c.env = map[string]string{}
		for _, key := range []string{"AGENT", "CONFIG", "DATA_DIR", "PROVIDER", "MODEL", "TIMEOUT", "ROLE", "ENDPOINT", "REGISTRY"} {
			c.env["ANRA_"+key] = "http://127.0.0.1:1/ignored"
		}
	})
	sc.Step(`^I serve an agent whose data-dir is missing$`, func() error {
		data, err := os.ReadFile(c.configPath())
		if err != nil {
			return err
		}
		text := strings.Replace(string(data), "data-dir = \"cli\"\n", "", 1)
		if err := os.WriteFile(c.configPath(), []byte(text), 0o600); err != nil {
			return err
		}
		c.execute("serve", "--config", c.configPath(), "--agent", "alice")
		return nil
	})
}

func (c *cliScenario) configPath() string { return filepath.Join(c.s.root, "agents.toml") }

// ensureConfig returns the scenario's TOML file, creating one with Alice and
// Bob when the scenario has not written its own. Agents require a lobby, so
// without a running directory the file points at an unused address.
func (c *cliScenario) ensureConfig() string {
	if _, err := os.Stat(c.configPath()); err != nil {
		if err := c.writeAgentConfig(""); err != nil {
			panic(err)
		}
	}
	return c.configPath()
}

func (c *cliScenario) writeAgentConfig(registry string) error {
	if registry == "" {
		registry = "http://127.0.0.1:1"
	}
	text := fmt.Sprintf("[lobby]\naddress = %q\n\n", registry)
	for _, id := range []string{"alice", "bob"} {
		text += fmt.Sprintf("[agents.%[1]s]\ndesc = \"%[1]s\"\ndata-dir = \"cli\"\nprovider = \"openai\"\ntimeout = \"2s\"\nendpoint = \"127.0.0.1:0\"\n\n", id)
	}
	return os.WriteFile(c.configPath(), []byte(text), 0o600)
}
