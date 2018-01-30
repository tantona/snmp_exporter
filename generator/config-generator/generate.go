package generator

import (
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/prometheus/common/log"
	"github.com/prometheus/snmp_exporter/config"
	yaml "gopkg.in/yaml.v2"
)

// GenerateConfig generates a snmp_exporter config and writes it to the outputPath
func GenerateConfig(nodes *Node, nameToNode map[string]*Node, outputPath string) {
	outputPath, err := filepath.Abs(outputPath)
	if err != nil {
		log.Fatal("Unable to determine absolute path for output")
	}

	content, err := ioutil.ReadFile("generator.yml")
	if err != nil {
		log.Fatalf("Error reading yml config: %s", err)
	}
	cfg := &Config{}
	err = yaml.Unmarshal(content, cfg)
	if err != nil {
		log.Fatalf("Error parsing yml config: %s", err)
	}

	outputConfig := config.Config{}
	for name, m := range cfg.Modules {
		log.Infof("Generating config for module %s", name)
		outputConfig[name] = generateConfigModule(m, nodes, nameToNode)
		outputConfig[name].WalkParams = m.WalkParams
		log.Infof("Generated %d metrics for module %s", len(outputConfig[name].Metrics), name)
	}

	config.DoNotHideSecrets = true
	out, err := yaml.Marshal(outputConfig)
	config.DoNotHideSecrets = false
	if err != nil {
		log.Fatalf("Error marshalling yml: %s", err)
	}

	// Check the generated config to catch auth/version issues.
	err = yaml.Unmarshal(out, &config.Config{})
	if err != nil {
		log.Fatalf("Error parsing generated config: %s", err)
	}

	f, err := os.Create(outputPath)
	if err != nil {
		log.Fatalf("Error opening output file: %s", err)
	}
	_, err = f.Write(out)
	if err != nil {
		log.Fatalf("Error writing to output file: %s", err)
	}
	log.Infof("Config written to %s", outputPath)
}
