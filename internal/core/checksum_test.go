package core

import "testing"

func TestChecksum_OrderIndependent(t *testing.T) {
	a := []File{{Path: "SKILL.md", Content: "x"}, {Path: "references/api.md", Content: "y"}}
	b := []File{{Path: "references/api.md", Content: "y"}, {Path: "SKILL.md", Content: "x"}}
	ca, err := Checksum(a)
	if err != nil {
		t.Fatal(err)
	}
	cb, err := Checksum(b)
	if err != nil {
		t.Fatal(err)
	}
	if ca != cb {
		t.Fatalf("排序不同应得到相同 checksum: %s != %s", ca, cb)
	}
}

func TestChecksum_ContentSensitive(t *testing.T) {
	a := []File{{Path: "SKILL.md", Content: "x"}}
	b := []File{{Path: "SKILL.md", Content: "y"}}
	ca, _ := Checksum(a)
	cb, _ := Checksum(b)
	if ca == cb {
		t.Fatal("内容不同必须得到不同 checksum")
	}
}

func TestChecksum_PathSensitive(t *testing.T) {
	a := []File{{Path: "SKILL.md", Content: "x"}}
	b := []File{{Path: "OTHER.md", Content: "x"}}
	ca, _ := Checksum(a)
	cb, _ := Checksum(b)
	if ca == cb {
		t.Fatal("路径不同必须得到不同 checksum")
	}
}

// Golden test：规范形式一旦改变会破坏所有已发布版本的校验，
// 因此这个值被钉死。若本用例失败，说明规范形式被改动了 —— 那是破坏性变更。
func TestChecksum_Golden(t *testing.T) {
	files := []File{
		{Path: "SKILL.md", Content: "---\ndescription: t\n---\n\nhi\n"},
		{Path: "references/a.md", Content: "a&b<c>d"},
	}
	got, err := Checksum(files)
	if err != nil {
		t.Fatal(err)
	}
	const want = "sha256:ae0242f60e592906b9aa8429a9e43be6e1493ed9eca304a687d4b9213ae99543"
	if got != want {
		t.Fatalf("规范形式发生变化\n got: %s\nwant: %s\n若这是有意的破坏性变更，请同步更新服务端与所有客户端。", got, want)
	}
}

func TestCanonicalJSON_NoHTMLEscape(t *testing.T) {
	b, err := CanonicalJSON([]File{{Path: "SKILL.md", Content: "a<b>c&d"}})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if want := `"a<b>c&d"`; !contains(s, want) {
		t.Fatalf("不应做 HTML 转义: %s", s)
	}
	if s[len(s)-1] == '\n' {
		t.Fatal("规范形式不应有尾随换行")
	}
}

func TestCanonicalFiles_DoesNotMutateInput(t *testing.T) {
	in := []File{{Path: "b"}, {Path: "a"}}
	_ = CanonicalFiles(in)
	if in[0].Path != "b" {
		t.Fatal("CanonicalFiles 不得修改入参")
	}
}

func TestVerifyChecksum(t *testing.T) {
	files := []File{{Path: "SKILL.md", Content: "x"}}
	sum, _ := Checksum(files)
	ok, err := VerifyChecksum(files, sum)
	if err != nil || !ok {
		t.Fatal("应校验通过")
	}
	ok, _ = VerifyChecksum(files, "sha256:deadbeef")
	if ok {
		t.Fatal("错误的 checksum 必须校验失败")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
