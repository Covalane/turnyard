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
