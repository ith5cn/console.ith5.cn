package resources

import (
	"strings"
	"testing"
)

func refs(files ...File) []FileRef {
	r, _ := ToRefs(files)
	return r
}

func TestChecksum_OrderIndependent(t *testing.T) {
	a := refs(File{Path: "SKILL.md", Content: "x"}, File{Path: "references/api.md", Content: "y"})
	b := refs(File{Path: "references/api.md", Content: "y"}, File{Path: "SKILL.md", Content: "x"})
	ca, _ := Checksum(a)
	cb, _ := Checksum(b)
	if ca != cb {
		t.Fatalf("排序不同应得到相同 checksum: %s != %s", ca, cb)
	}
}

func TestChecksum_ContentAndPathSensitive(t *testing.T) {
	base, _ := Checksum(refs(File{Path: "SKILL.md", Content: "x"}))
	content, _ := Checksum(refs(File{Path: "SKILL.md", Content: "y"}))
	path, _ := Checksum(refs(File{Path: "OTHER.md", Content: "x"}))
	if base == content || base == path {
		t.Fatal("内容或路径不同必须得到不同 checksum")
	}
}

// Golden test：规范形式一旦改变会破坏所有已发布版本的校验，因此这个值被钉死。
// 若本用例失败，说明规范形式被改动了——那是破坏性变更。
func TestChecksum_Golden(t *testing.T) {
	got, err := Checksum(refs(
		File{Path: "SKILL.md", Content: "---\ndescription: t\n---\n\nhi\n"},
		File{Path: "references/a.md", Content: "a&b<c>d"},
	))
	if err != nil {
		t.Fatal(err)
	}
	const want = "sha256:5c0008c714db136cb38f6fc7513636cd93c712fff8ecb730a0b1e694efe71ae0"
	if got != want {
		t.Fatalf("规范形式发生变化\n got: %s\nwant: %s\n若这是有意的破坏性变更，请同步更新服务端与所有客户端。", got, want)
	}
}

func TestCanonicalJSON_NoHTMLEscapeNoTrailingNewline(t *testing.T) {
	b, err := CanonicalJSON(refs(File{Path: "a<b>.md", Content: "x"}))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `"a<b>.md"`) {
		t.Fatalf("不应做 HTML 转义: %s", s)
	}
	if strings.HasSuffix(s, "\n") {
		t.Fatal("规范形式不应有尾随换行")
	}
	if !strings.HasPrefix(s, `[{"path":"a<b>.md","sha256":"sha256:`) {
		t.Fatalf("键顺序应为 path、sha256、size: %s", s)
	}
}

func TestCanonicalRefs_DoesNotMutateInput(t *testing.T) {
	in := []FileRef{{Path: "b"}, {Path: "a"}}
	_ = CanonicalRefs(in)
	if in[0].Path != "b" {
		t.Fatal("CanonicalRefs 不得修改入参")
	}
}

func TestToRefs_DedupesBlobs(t *testing.T) {
	r, blobs := ToRefs([]File{{Path: "a.md", Content: "same"}, {Path: "b.md", Content: "same"}})
	if len(r) != 2 || len(blobs) != 1 {
		t.Fatalf("相同内容应共用一个 blob: refs=%d blobs=%d", len(r), len(blobs))
	}
	if r[0].SHA256 != r[1].SHA256 || r[0].Size != 4 {
		t.Fatalf("哈希与大小不对: %+v", r)
	}
}

func TestVerifyChecksum(t *testing.T) {
	r := refs(File{Path: "SKILL.md", Content: "x"})
	sum, _ := Checksum(r)
	if ok, _ := VerifyChecksum(r, sum); !ok {
		t.Fatal("应校验通过")
	}
	if ok, _ := VerifyChecksum(r, "sha256:deadbeef"); ok {
		t.Fatal("错误的 checksum 必须校验失败")
	}
}
