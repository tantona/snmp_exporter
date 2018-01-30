package generator

import (
	"regexp"
	"sort"
	"strings"

	"github.com/prometheus/common/log"
	"github.com/prometheus/snmp_exporter/config"
)

// WalkNode is a helper to walk MIB nodes.
func WalkNode(n *Node, f func(n *Node)) {
	f(n)
	for _, c := range n.Children {
		WalkNode(c, f)
	}
}

// PrepareTree transforms the tree
func PrepareTree(nodes *Node) map[string]*Node {
	// Build a map from names and oids to nodes.
	nameToNode := map[string]*Node{}
	WalkNode(nodes, func(n *Node) {
		nameToNode[n.Oid] = n
		nameToNode[n.Label] = n
	})

	// Trim down description to first sentance, removing extra whitespace.
	WalkNode(nodes, func(n *Node) {
		s := strings.Join(strings.Fields(n.Description), " ")
		n.Description = strings.Split(s, ". ")[0]
	})

	// Fix indexes to "INTEGER" rather than an object name.
	// Example: snSlotsEntry in LANOPTICS-HUB-MIB
	WalkNode(nodes, func(n *Node) {
		indexes := []string{}
		for _, i := range n.Indexes {
			if i == "INTEGER" {
				// Use the TableEntry name.
				indexes = append(indexes, n.Label)
			} else {
				indexes = append(indexes, i)
			}
		}
		n.Indexes = indexes
	})

	// Copy over indexes based on augments.
	WalkNode(nodes, func(n *Node) {
		if n.Augments == "" {
			return
		}
		augmented, ok := nameToNode[n.Augments]
		if !ok {
			log.Warnf("Can't find augmenting oid %s for %s", n.Augments, n.Label)
			return
		}
		for _, c := range n.Children {
			c.Indexes = augmented.Indexes
		}
		n.Indexes = augmented.Indexes
	})

	// Copy indexes from table entries down to the entries.
	WalkNode(nodes, func(n *Node) {
		if len(n.Indexes) != 0 {
			for _, c := range n.Children {
				c.Indexes = n.Indexes
			}
		}
	})

	// Include both ASCII and UTF-8 in DisplayString, even though DisplayString
	// is technically only ASCII.
	displayStringRe := regexp.MustCompile(`\d+[at]`)

	// Set type on MAC addresses and strings.
	WalkNode(nodes, func(n *Node) {
		// RFC 2579
		switch n.Hint {
		case "1x:":
			n.Type = "PhysAddress48"
		}
		if displayStringRe.MatchString(n.Hint) {
			n.Type = "DisplayString"
		}

		// Some MIBs refer to RFC1213 for this, which is too
		// old to have the right hint set.
		if n.TextualConvention == "DisplayString" {
			n.Type = "DisplayString"
		}
	})

	return nameToNode
}

func metricType(t string) (string, bool) {
	switch t {
	case "INTEGER", "GAUGE", "TIMETICKS", "UINTEGER", "UNSIGNED32", "INTEGER32":
		return "gauge", true
	case "COUNTER", "COUNTER64":
		return "counter", true
	case "OCTETSTR", "BITSTRING":
		return "OctetString", true
	case "IPADDR":
		return "IpAddr", true
	case "NETADDR":
		// TODO: Not sure about this one.
		return "InetAddress", true
	case "PhysAddress48", "DisplayString":
		return t, true
	default:
		// Unsupported type.
		return "", false
	}
}

func metricAccess(a string) bool {
	switch a {
	case "ACCESS_READONLY", "ACCESS_READWRITE", "ACCESS_CREATE", "ACCESS_NOACCESS":
		return true
	default:
		// the others are inaccessible metrics.
		return false
	}
}

// Reduce a set of overlapping OID subtrees.
func minimizeOids(oids []string) []string {
	sort.Strings(oids)
	prevOid := ""
	minimized := []string{}
	for _, oid := range oids {
		if !strings.HasPrefix(oid+".", prevOid) || prevOid == "" {
			minimized = append(minimized, oid)
			prevOid = oid + "."
		}
	}
	return minimized
}

func generateConfigModule(cfg *ModuleConfig, node *Node, nameToNode map[string]*Node) *config.Module {
	out := &config.Module{}
	needToWalk := map[string]struct{}{}

	// Remove redundant OIDs to be walked.
	toWalk := []string{}
	for _, oid := range cfg.Walk {
		node, ok := nameToNode[oid]
		if !ok {
			log.Fatalf("Cannot find oid '%s' to walk", oid)
		}
		toWalk = append(toWalk, node.Oid)
	}
	toWalk = minimizeOids(toWalk)

	// Find all the usable metrics.
	for _, oid := range toWalk {
		node := nameToNode[oid]
		needToWalk[node.Oid] = struct{}{}
		WalkNode(node, func(n *Node) {
			t, ok := metricType(n.Type)
			if !ok {
				return // Unsupported type.
			}

			if !metricAccess(n.Access) {
				return // Inaccessible metrics.
			}

			metric := &config.Metric{
				Name:    sanitizeLabelName(n.Label),
				Oid:     n.Oid,
				Type:    t,
				Help:    n.Description + " - " + n.Oid,
				Indexes: []*config.Index{},
				Lookups: []*config.Lookup{},
			}
			for _, i := range n.Indexes {
				index := &config.Index{Labelname: i}
				indexNode, ok := nameToNode[i]
				if !ok {
					log.Warnf("Error, can't find index %s for node %s", i, n.Label)
					return
				}
				index.Type, ok = metricType(indexNode.Type)
				if !ok {
					log.Warnf("Error, can't handle index type %s for node %s", indexNode.Type, n.Label)
					return
				}
				index.FixedSize = indexNode.FixedSize
				metric.Indexes = append(metric.Indexes, index)
			}
			out.Metrics = append(out.Metrics, metric)
		})
	}

	// Apply lookups.
	for _, lookup := range cfg.Lookups {
		for _, metric := range out.Metrics {
			for _, index := range metric.Indexes {
				if index.Labelname == lookup.OldIndex {
					if _, ok := nameToNode[lookup.NewIndex]; !ok {
						log.Fatalf("Unknown index '%s'", lookup.NewIndex)
					}
					indexNode := nameToNode[lookup.NewIndex]
					// Avoid leaving the old labelname around.
					index.Labelname = sanitizeLabelName(indexNode.Label)
					typ, ok := metricType(indexNode.Type)
					if !ok {
						log.Fatalf("Unknown index type %s for %s", indexNode.Type, lookup.NewIndex)
					}
					metric.Lookups = append(metric.Lookups, &config.Lookup{
						Labels:    []string{sanitizeLabelName(indexNode.Label)},
						Labelname: sanitizeLabelName(indexNode.Label),
						Type:      typ,
						Oid:       indexNode.Oid,
					})
					// Make sure we walk the lookup OID
					needToWalk[indexNode.Oid] = struct{}{}
				}
			}
		}
	}

	// Apply module config overrides to their corresponding metrics.
	for name, params := range cfg.Overrides {
		for _, metric := range out.Metrics {
			if name == metric.Name || name == metric.Oid {
				metric.RegexpExtracts = params.RegexpExtracts
			}
		}
	}

	oids := []string{}
	for k, _ := range needToWalk {
		oids = append(oids, k)
	}
	// Remove redundant OIDs to be walked.
	out.Walk = minimizeOids(oids)
	return out
}

var (
	invalidLabelCharRE = regexp.MustCompile(`[^a-zA-Z0-9_]`)
)

func sanitizeLabelName(name string) string {
	return invalidLabelCharRE.ReplaceAllString(name, "_")
}
