package resources

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// File 是上传或校验时携带正文的文件。
type File struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// FileRef 是版本清单中的一条：只有路径、内容哈希与大小，正文在 blob 存储。
type FileRef struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int    `json:"size"`
}

// BlobSum 返回单个文件正文的 "sha256:" 前缀十六进制摘要。这是 blob 的寻址键。
func BlobSum(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ToRefs 把带正文的文件集合转成清单，同时返回每个哈希对应的正文，供写入 blob 存储。
func ToRefs(files []File) ([]FileRef, map[string][]byte) {
	refs := make([]FileRef, 0, len(files))
	blobs := make(map[string][]byte, len(files))
	for _, f := range files {
		b := []byte(f.Content)
		sum := BlobSum(b)
		refs = append(refs, FileRef{Path: f.Path, SHA256: sum, Size: len(b)})
		blobs[sum] = b
	}
	return CanonicalRefs(refs), blobs
}

// CanonicalRefs 返回清单的规范形式：按 Path 升序排序的副本。
//
// 规范化是 checksum 可复现的前提。服务端在发布时计算一次，
// 客户端在落盘前重算一次，两者必须逐字节一致。
func CanonicalRefs(refs []FileRef) []FileRef {
	out := make([]FileRef, len(refs))
	copy(out, refs)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// CanonicalJSON 把清单编码为规范 JSON。
//
// 规范定义（跨语言可复现，故显式写死，不依赖 encoding/json 的默认行为）：
//   - 顶层是数组，元素按 path 升序
//   - 每个元素的键顺序固定为 path、sha256、size
//   - 不做 HTML 转义
//   - 无缩进、无尾随换行
func CanonicalJSON(refs []FileRef) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(CanonicalRefs(refs)); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// Checksum 返回一个版本的 "sha256:" 前缀摘要，由清单的规范 JSON 导出。
func Checksum(refs []FileRef) (string, error) {
	b, err := CanonicalJSON(refs)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// VerifyChecksum 校验清单是否匹配声明的 checksum。
func VerifyChecksum(refs []FileRef, want string) (bool, error) {
	got, err := Checksum(refs)
	if err != nil {
		return false, err
	}
	return got == want, nil
}
