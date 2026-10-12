package seirofile

import (
	"bytes"
	"fmt"
	"strconv"

	"go.starlark.net/starlark"
	"go.yaml.in/yaml/v3"
)

// toYAML renders a Starlark value as a YAML document, keeping dict insertion order.
func toYAML(v starlark.Value) (string, error) {
	n, err := toNode(v)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(n); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func toNode(v starlark.Value) (*yaml.Node, error) {
	scalar := func(tag, value string) *yaml.Node {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value}
	}
	switch v := v.(type) {
	case starlark.NoneType:
		return scalar("!!null", "null"), nil
	case starlark.Bool:
		return scalar("!!bool", strconv.FormatBool(bool(v))), nil
	case starlark.Int:
		return scalar("!!int", v.String()), nil
	case starlark.Float:
		return scalar("!!float", strconv.FormatFloat(float64(v), 'g', -1, 64)), nil
	case starlark.String:
		// Node.Encode quotes strings that YAML 1.1 parsers such as cloud-init's
		// PyYAML would read as other types (e.g. "yes", "on", "1").
		n := &yaml.Node{}
		if err := n.Encode(string(v)); err != nil {
			return nil, err
		}
		return n, nil
	case *starlark.List, starlark.Tuple:
		seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		if err := each(v.(starlark.Iterable), func(e starlark.Value) error {
			n, err := toNode(e)
			if err != nil {
				return err
			}
			seq.Content = append(seq.Content, n)
			return nil
		}); err != nil {
			return nil, err
		}
		return seq, nil
	case *starlark.Dict:
		m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		for _, item := range v.Items() {
			k, ok := item[0].(starlark.String)
			if !ok {
				return nil, fmt.Errorf("dict keys must be strings, got %s", item[0].Type())
			}
			val, err := toNode(item[1])
			if err != nil {
				return nil, fmt.Errorf("%s: %w", string(k), err)
			}
			key, err := toNode(k)
			if err != nil {
				return nil, err
			}
			m.Content = append(m.Content, key, val)
		}
		return m, nil
	default:
		return nil, fmt.Errorf("cannot convert %s to YAML", v.Type())
	}
}
