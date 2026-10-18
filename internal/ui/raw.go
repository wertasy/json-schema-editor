package ui

// Raw-editor text syntax: JSON by default, YAML for YAML documents. The
// caller supplies the YAML codec because only it knows the live format
// session (comments included).

import (
	"bytes"
	"io"

	"github.com/wertasy/json-schema-editor/internal/jsondoc"
)

func (o Options) rawLabel() string {
	if o.Raw.Label != "" {
		return o.Raw.Label
	}
	return "JSON"
}

func (o Options) rawExt() string {
	if o.Raw.Ext != "" {
		return o.Raw.Ext
	}
	return ".json"
}

func (o Options) encodeRawDoc(w io.Writer, doc *jsondoc.Node) error {
	if o.Raw.MarshalDoc != nil {
		return o.Raw.MarshalDoc(w)
	}
	return doc.Write(w)
}

func (o Options) decodeRawDoc(data []byte) (*jsondoc.Node, error) {
	if o.Raw.AdoptDoc != nil {
		return o.Raw.AdoptDoc(data)
	}
	if o.Raw.UnmarshalDoc != nil {
		return o.Raw.UnmarshalDoc(data)
	}
	return jsondoc.Parse(bytes.NewReader(data))
}

func (o Options) encodeRawValue(w io.Writer, n *jsondoc.Node) error {
	if o.Raw.MarshalValue != nil {
		return o.Raw.MarshalValue(w, n)
	}
	return n.Write(w)
}

func (o Options) decodeRawValue(data []byte) (*jsondoc.Node, error) {
	if o.Raw.UnmarshalValue != nil {
		return o.Raw.UnmarshalValue(data)
	}
	return jsondoc.Parse(bytes.NewReader(data))
}
