package contracts

import "testing"

func TestConnectorURIPrefixCannotEscapeOrChangeAuthority(t *testing.T) {
	spec := ArtifactConnectorSpec{URIPrefix: "oss://bucket/allowed/"}
	for _, uri := range []string{
		"oss://bucket/allowed/report.pdf",
		"oss://bucket/allowed/sub/report.pdf",
		"oss://bucket/allowed/报告.pdf",
	} {
		if !ValidConnectorURI(spec, uri) {
			t.Fatalf("valid object rejected: %s", uri)
		}
	}
	for _, uri := range []string{
		"oss://bucket/allowed/../private/report.pdf",
		"oss://bucket/allowed/%2e%2e/private/report.pdf",
		"oss://bucket/allowed/sub//report.pdf",
		"oss://bucket/allowed\\..\\private\\report.pdf",
		"oss://bucket.evil/allowed/report.pdf",
		"oss://user@bucket/allowed/report.pdf",
		"oss://bucket/allowed2/report.pdf",
		"oss://bucket/allowed/report.pdf?token=secret",
		"oss://bucket/allowed/",
	} {
		if ValidConnectorURI(spec, uri) {
			t.Fatalf("out-of-bound object accepted: %s", uri)
		}
	}
	for _, prefix := range []string{"oss://bucket", "oss://bucket/allowed", "oss://bucket/allowed/../", "oss://user@bucket/allowed/"} {
		if ValidConnectorPrefix(prefix) {
			t.Fatalf("unsafe prefix accepted: %s", prefix)
		}
	}
}

func TestConnectorCatalogSeparatesReadAndWriteCapabilities(t *testing.T) {
	catalog := connectorCatalog{"read": {URIPrefix: "oss://bucket/allowed/"},
		"write": {URIPrefix: "oss://bucket/allowed/", PutArgv: []string{"object-transfer", "create"}, PutMode: ConnectorPutCreateOnly, NotFoundExitCodes: []int{44}}}
	if !catalog.canRead("read", "oss://bucket/allowed/input.png") || catalog.canWrite("read", "oss://bucket/allowed/{sha256}.png") {
		t.Fatal("read-only connector gained write access or lost read access")
	}
	if !catalog.canWrite("write", "oss://bucket/allowed/{sha256}.png") {
		t.Fatal("write connector rejected a content-addressed destination")
	}
	for _, uri := range []string{"oss://bucket/allowed/output.png", "oss://bucket/private/{sha256}.png", "oss://bucket/allowed/{sha256}/{sha256}.png"} {
		if catalog.canWrite("write", uri) {
			t.Fatalf("invalid connector destination was accepted: %s", uri)
		}
	}
	if catalog.canRead("missing", "oss://bucket/allowed/input.png") {
		t.Fatal("missing connector was accepted")
	}
}

func TestConnectorDeliveryRequiresExplicitSafeCommandContract(t *testing.T) {
	valid := ArtifactConnectorSpec{ID: "object", PutArgv: []string{"transfer", "create", "{uri}", "{file}"},
		PutMode: ConnectorPutCreateOnly, NotFoundExitCodes: []int{44}}
	if err := ValidateConnectorDelivery(valid); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*ArtifactConnectorSpec){
		func(spec *ArtifactConnectorSpec) { spec.PutMode = "" },
		func(spec *ArtifactConnectorSpec) { spec.PutMode = "overwrite" },
		func(spec *ArtifactConnectorSpec) { spec.NotFoundExitCodes = nil },
		func(spec *ArtifactConnectorSpec) { spec.NotFoundExitCodes = []int{0} },
		func(spec *ArtifactConnectorSpec) { spec.NotFoundExitCodes = []int{44, 44} },
	} {
		spec := valid
		change(&spec)
		if err := ValidateConnectorDelivery(spec); err == nil {
			t.Fatalf("unsafe connector accepted: %+v", spec)
		}
	}
}
