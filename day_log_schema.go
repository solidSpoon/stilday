package main

import (
	"encoding/json"
	"strings"
)

// dayLogPlaceholder marks where the rendered arguments go.
const dayLogPlaceholder = "{{stilday_save_day_args}}"

// schemaNode is just enough type to write both a type definition and a JSON
// Schema. Field order is the order they are written in.
type schemaNode struct {
	kind   string // object, array, string, number, boolean
	doc    string
	values []string // for a string enum
	fields []schemaField
	items  *schemaNode
}

type schemaField struct {
	name string
	node schemaNode
	opt  bool
}

func objectNode(doc string, fields ...schemaField) schemaNode {
	return schemaNode{kind: "object", doc: doc, fields: fields}
}

func arrayNode(doc string, item schemaNode) schemaNode {
	return schemaNode{kind: "array", doc: doc, items: &item}
}

func stringNode(doc string) schemaNode { return schemaNode{kind: "string", doc: doc} }

// enumNode is a string with a fixed set of values.
func enumNode(doc string, values ...string) schemaNode {
	return schemaNode{kind: "string", doc: doc, values: values}
}

func numberNode(doc string) schemaNode { return schemaNode{kind: "number", doc: doc} }

// stilday_save_day takes one object: the day's complete log. The shape is
// written down once, here, and rendered twice:
//
//   - renderDayLog writes the prompt's 记录 section, as a type definition
//     with the docs as comments;
//   - dayLogSchemaJSON is passed to the Pi extension as
//     STILDAY_DAY_LOG_SCHEMA, where pi/stilday.ts turns it into the tool's
//     TypeBox parameters.
//
// So the prompt cannot describe a shape the tool does not take. Edit
// dayLogArgs, not the prompt: the placeholder there is filled in at runtime.

var dayLogArgs = objectNode("",
	schemaField{name: "entries", node: arrayNode(
		"当天完整的日志。每次调用都会替换当天已有的记录，所以没改的条目也要一起发。",
		objectNode("",
			schemaField{name: "kind", node: enumNode(`事件类型，只能取下面这些值：
observation：看到、注意到的事，以及据此得出的结论；推断要标明（「看起来」「可能」）。没别的类型合适时用它。
activity：做过的事——出门、散步、运动、看展、旅游、见人；地点写进 summary。
food：吃了什么、做了什么吃的、去了哪家店。
health：看病、吃药、体检、受伤、身体不舒服。
plan：未来的安排——车票、航班、酒店、约会、行程。summary 里把截图里有的日期、时间、班次、从哪到哪、住哪写清楚，能直接照着安排；不要只写「订票」这个动作。
interest：看到想以后跟进的东西——技术、框架、帖子、想法；写清楚是什么、为什么感兴趣。`,
				"observation", "activity", "food", "health", "plan", "interest",
			)},
			schemaField{name: "summary", node: stringNode(
				"一句话说明发生了什么，用用户的语言。occurred_at 已经记了发生时间，summary 里不用再写一遍；但事情涉及的日期和时间（比如发车、入住、截止时间）要写清楚。",
			)},
			schemaField{name: "occurred_at", node: stringNode(
				"这件事发生的时间，RFC 3339，例如 2026-10-24T18:30:00+08:00。截图通常就是截图保存的时间，不是截图里写的发车、入住等时间。",
			)},
		),
	)},
)

// renderDayLog is the prompt section: the arguments as a type definition.
func renderDayLog() string {
	var b strings.Builder
	b.WriteString("```ts\n")
	b.WriteString("type StildaySaveDayArgs = ")
	dayLogArgs.writeTS(&b, "")
	b.WriteString(";\n```")
	return b.String()
}

func (n schemaNode) writeTS(b *strings.Builder, indent string) {
	switch n.kind {
	case "object":
		if len(n.fields) == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteString("{\n")
		for _, f := range n.fields {
			writeDocComment(b, indent+"  ", f.node.doc)
			b.WriteString(indent + "  " + f.name)
			if f.opt {
				b.WriteString("?")
			}
			b.WriteString(": ")
			f.node.writeTS(b, indent+"  ")
			b.WriteString(";\n")
		}
		b.WriteString(indent + "}")
	case "array":
		b.WriteString("Array<")
		n.items.writeTS(b, indent)
		b.WriteString(">")
	case "string":
		if len(n.values) > 0 {
			for i, v := range n.values {
				if i > 0 {
					b.WriteString(" | ")
				}
				b.WriteString(jsonString(v))
			}
			return
		}
		b.WriteString("string")
	default:
		b.WriteString(n.kind)
	}
}

func writeDocComment(b *strings.Builder, indent, doc string) {
	if doc == "" {
		return
	}
	if !strings.Contains(doc, "\n") {
		b.WriteString(indent + "/** " + doc + " */\n")
		return
	}
	b.WriteString(indent + "/**\n")
	for _, line := range strings.Split(doc, "\n") {
		b.WriteString(indent + " * " + line + "\n")
	}
	b.WriteString(indent + " */\n")
}

// dayLogSchemaJSON is the same tree as a JSON Schema, for the extension.
func dayLogSchemaJSON() string {
	var b strings.Builder
	dayLogArgs.writeJSON(&b)
	return b.String()
}

func (n schemaNode) writeJSON(b *strings.Builder) {
	b.WriteString(`{"type":` + jsonString(n.kind))
	if n.doc != "" {
		b.WriteString(`,"description":` + jsonString(n.doc))
	}
	if len(n.values) > 0 {
		b.WriteString(`,"enum":[`)
		for i, v := range n.values {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(jsonString(v))
		}
		b.WriteString("]")
	}
	switch n.kind {
	case "object":
		if len(n.fields) > 0 {
			b.WriteString(`,"properties":{`)
			for i, f := range n.fields {
				if i > 0 {
					b.WriteString(",")
				}
				b.WriteString(jsonString(f.name) + ":")
				f.node.writeJSON(b)
			}
			b.WriteString("}")
			var required []string
			for _, f := range n.fields {
				if !f.opt {
					required = append(required, jsonString(f.name))
				}
			}
			if len(required) > 0 {
				b.WriteString(`,"required":[` + strings.Join(required, ",") + "]")
			}
		}
	case "array":
		b.WriteString(`,"items":`)
		n.items.writeJSON(b)
	}
	b.WriteString("}")
}

func jsonString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err) // a string always marshals
	}
	return string(b)
}
