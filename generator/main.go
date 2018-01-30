package main

import (
	"fmt"
	"strings"

	"github.com/prometheus/common/log"
	generator "github.com/prometheus/snmp_exporter/generator/config-generator"
	"gopkg.in/alecthomas/kingpin.v2"
)

var (
	generateCommand    = kingpin.Command("generate", "Generate snmp.yml from generator.yml")
	outputPath         = generateCommand.Flag("output-path", "Path to to write resulting config file").Default("snmp.yml").Short('o').String()
	parseErrorsCommand = kingpin.Command("parse_errors", "Debug: Print the parse errors output by NetSNMP")
	dumpCommand        = kingpin.Command("dump", "Debug: Dump the parsed and prepared MIBs")
)

func main() {
	log.AddFlags(kingpin.CommandLine)
	kingpin.HelpFlag.Short('h')
	command := kingpin.Parse()

	parseErrors := generator.InitSNMP()
	log.Warnf("NetSNMP reported %d parse errors", len(strings.Split(parseErrors, "\n")))

	nodes := generator.GetMIBTree()
	nameToNode := generator.PrepareTree(nodes)

	switch command {
	case generateCommand.FullCommand():
		generator.GenerateConfig(nodes, nameToNode, *outputPath)
	case parseErrorsCommand.FullCommand():
		fmt.Println(parseErrors)
	case dumpCommand.FullCommand():
		generator.WalkNode(nodes, func(n *generator.Node) {
			t := n.Type
			if n.FixedSize != 0 {
				t = fmt.Sprintf("%s(%d)", n.Type, n.FixedSize)
			}
			fmt.Printf("%s %s %s %q %q %s %s\n", n.Oid, n.Label, t, n.TextualConvention, n.Hint, n.Indexes, n.Description)
		})
	}
}
