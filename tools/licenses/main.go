// Command licenses collects unmodified license texts for linked Go modules.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

type module struct {
	Path, Version, Dir string
	Main               bool
	Replace            *module
}

func main() {
	output := flag.String("out", "bin/licenses/GO_THIRD_PARTY_LICENSES.txt", "output text file")
	flag.Parse()
	if err := collect(*output); err != nil {
		log.Fatal(err)
	}
}

func collect(output string) error {
	cmd := exec.Command("go", "list", "-deps", "-json", "./backend/cmd/controlplane", "./agent/cmd/agent", "./adb-deployer/cmd/adb-deployer")
	cmd.Stderr = os.Stderr
	data, err := cmd.Output()
	if err != nil {
		return err
	}
	modules := map[string]module{}
	decoder := json.NewDecoder(bytes.NewReader(data))
	for {
		var pkg struct{ Module *module }
		if err := decoder.Decode(&pkg); err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		if pkg.Module == nil || pkg.Module.Main {
			continue
		}
		m := *pkg.Module
		if m.Replace != nil {
			m.Dir = m.Replace.Dir
		}
		modules[m.Path] = m
	}
	keys := make([]string, 0, len(modules))
	for key := range modules {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var result bytes.Buffer
	result.WriteString("ScrcpyCat Go third-party licenses\n")
	appendFile := func(title, file string) error {
		text, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		fmt.Fprintf(&result, "\n%s\n%s\n%s\n%s\n", strings.Repeat("=", 78), title, strings.Repeat("=", 78), text)
		return nil
	}
	if err := appendFile("Go runtime and standard library", filepath.Join(runtime.GOROOT(), "LICENSE")); err != nil {
		return err
	}
	for _, key := range keys {
		m := modules[key]
		entries, err := os.ReadDir(m.Dir)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		foundLicense := false
		for _, entry := range entries {
			base := strings.ToUpper(strings.SplitN(entry.Name(), ".", 2)[0])
			isLicense := base == "LICENSE" || base == "LICENCE" || base == "COPYING"
			if entry.IsDir() || !(isLicense || base == "NOTICE" || base == "PATENTS") {
				continue
			}
			if err := appendFile(m.Path+"@"+m.Version+" — "+entry.Name(), filepath.Join(m.Dir, entry.Name())); err != nil {
				return err
			}
			foundLicense = foundLicense || isLicense
		}
		if !foundLicense {
			return fmt.Errorf("missing license for %s@%s", m.Path, m.Version)
		}
	}
	if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
		return err
	}
	return os.WriteFile(output, result.Bytes(), 0644)
}
