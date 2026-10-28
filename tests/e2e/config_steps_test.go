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
	sc.Step(`^a TOML CLI configuration for the team directory$`, func() error { return c.writeAgentConfig(team.registry.peer) })
	sc.Step(`^I run the configured CLI agent "([^"]*)" with "([^"]*)"$`, func(id, text string) error {
		c.execute("run", "--config", c.configPath(), "--agent", id, text)
		return c.err
	})
	sc.Step(`^the configured CLI agent "([^"]*)" is serving$`, func(id string) error {
		p := &cliScenario{s: c.s}
		team.agents[id] = p
		return p.startProcess(id, "--config", c.configPath(), "serve", "--agent", id)
	})
	sc.Step(`^the configured CLI agent "([^"]*)" sends "([^"]*)" to "([^"]*)"$`, func(id, text, to string) error {
		c.execute("ask", "--config", c.configPath(), "--agent", id, "ask "+to+": "+text)
		return c.err
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
	sc.Step(`^I run the "([^"]*)" command without --config$`, func(command string) {
		args := []string{command, "--agent", "alice"}
		if command == "send" {
			args = append(args, "--to", "bob")
		}
		if command == "run" || command == "ask" || command == "send" {
			args = append(args, "hello")
		}
		c.execute(args...)
	})
	sc.Step(`^I pass the removed setting flag "([^"]*)" to a configured agent$`, func(flag string) {
		c.execute("run", "--agent", "alice", "--config", c.ensureConfig(), flag, "x", "hello")
	})
	sc.Step(`^the environment tries to override every agent setting$`, func() {
		c.env = map[string]string{}
		for _, key := range []string{"AGENT", "CONFIG", "DATA_DIR", "PROVIDER", "MODEL", "TIMEOUT", "ROLE", "ENDPOINT", "REGISTRY", "SESSION", "API_BASE", "TO", "PEER"} {
			c.env["ANRA_"+key] = "http://127.0.0.1:1/ignored"
		}
	})
	sc.Step(`^I run the CLI with TOML configuration problem "([^"]*)"$`, c.invalidConfig)
	sc.Step(`^I run the "([^"]*)" command with "([^"]*)" (missing|empty|blank) for agent "([^"]*)"$`, func(command, key, problem, id string) error {
		data, err := os.ReadFile(c.configPath())
		if err != nil {
			return err
		}
		pattern := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + ` = [^\n]*\n`)
		if !pattern.Match(data) {
			return fmt.Errorf("configuration has no %s setting", key)
		}
		replacement := ""
		if problem == "empty" {
			replacement = key + " = \"\"\n"
		} else if problem == "blank" {
			replacement = key + " = \"   \"\n"
		}
		text := pattern.ReplaceAllString(string(data), replacement)
		if err := os.WriteFile(c.configPath(), []byte(text), 0o600); err != nil {
			return err
		}
		args := []string{command, "--config", c.configPath(), "--agent", id}
		if command != "serve" {
			args = append(args, "hello")
		}
		c.execute(args...)
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
		text += fmt.Sprintf("[agents.%[1]s]\ndesc = \"%[1]s\"\ndata-dir = \"cli\"\nprovider = \"deepseek\"\ntimeout = \"2s\"\nendpoint = \"127.0.0.1:0\"\n\n", id)
	}
	return os.WriteFile(c.configPath(), []byte(text), 0o600)
}

func (c *cliScenario) invalidConfig(problem string) error {
	if err := c.writeAgentConfig(""); err != nil {
		return err
	}
	data, err := os.ReadFile(c.configPath())
	if err != nil {
		return err
	}
	text := string(data)
	args := []string{"run", "--config", c.configPath(), "--agent", "alice"}
	switch problem {
	case "missing file":
		if err := os.Remove(c.configPath()); err != nil {
			return err
		}
	case "malformed TOML":
		text += "\n[broken\n"
	case "wrong setting type":
		text = strings.ReplaceAll(text, `provider = "deepseek"`, `provider = 123`)
	case "wrong lobby address type":
		text = strings.Replace(text, `address = "http://127.0.0.1:1"`, "address = 123", 1)
	case "unknown lobby setting":
		text = strings.Replace(text, "address =", "adress =", 1)
	case "wrong role type":
		text += "\nrole = 123\n"
	case "wrong desc type":
		text += "\ndesc = 123\n"
	case "wrong endpoint type":
		text += "\nendpoint = 123\n"
	case "unknown setting":
		text += "\nruner = 'echo'\n"
	case "agent peer setting":
		text += "\npeer = 'http://127.0.0.1:1'\n"
	case "agent session setting":
		text += "\nsession = 'research'\n"
	case "agent registry setting":
		text += "\nregistry = 'http://127.0.0.1:1'\n"
	case "defaults section":
		text += "\n[defaults]\nprovider = 'deepseek'\n"
	case "default agent setting":
		text = "default-agent = 'alice'\n" + text
	case "no agents":
		text = "[lobby]\naddress = 'http://127.0.0.1:1'\n"
	case "unknown provider":
		text = strings.ReplaceAll(text, `provider = "deepseek"`, `provider = "unknown"`)
	case "missing lobby":
		text = strings.Replace(text, "[lobby]\naddress = \"http://127.0.0.1:1\"\n\n", "", 1)
	case "invalid public address":
		text = strings.ReplaceAll(text, `endpoint = "127.0.0.1:0"`, `endpoint = "127.0.0.1:0"`+"\naddress = 'http://127.0.0.1:0'")
	case "invalid lobby address":
		text = strings.Replace(text, "http://127.0.0.1:1", "ftp://example.test", 1)
	case "unknown selected agent":
		args = append(args, "--agent", "charlie")
	case "invalid agent name":
		text += "\n[agents.'../outside']\nprovider = 'deepseek'\n"
	case "invalid duration":
		text = strings.Replace(text, `timeout = "2s"`, `timeout = "soon"`, 1)
	case "zero timeout":
		text = strings.Replace(text, `timeout = "2s"`, `timeout = "0s"`, 1)
	default:
		return fmt.Errorf("unknown TOML problem %q", problem)
	}
	if problem != "missing file" {
		if err := os.WriteFile(c.configPath(), []byte(text), 0o600); err != nil {
			return err
		}
	}
	c.execute(append(args, "hello")...)
	return nil
}
