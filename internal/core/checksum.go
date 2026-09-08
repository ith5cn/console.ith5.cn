package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
)

// CanonicalFiles 返回文件集合的规范形式：按 Path 升序排序的副本。
//
// 规范化是 checksum 可复现的前提。服务端在发布时计算一次，
// 客户端在落盘前重算一次，两者必须逐字节一致（技术方案 §8.2.2）。
func CanonicalFiles(files []File) []File {
	out := make([]File, len(files))
	copy(out, files)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// CanonicalJSON 把文件集合编码为规范 JSON。
//
// 规范定义（跨语言可复现，故显式写死，不依赖 encoding/json 的默认行为）：
//   - 顶层是数组，元素按 path 升序
//   - 每个元素的键顺序固定为 path、content
//   - 不做 HTML 转义（< > & 保持原样）
//   - 无缩进、无尾随换行
func CanonicalJSON(files []File) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(CanonicalFiles(files)); err != nil {
		return nil, err
	}
	// Encoder.Encode 会追加一个换行，规范形式不包含它。
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// Checksum 返回 "sha256:" 前缀的十六进制摘要。
func Checksum(files []File) (string, error) {
	b, err := CanonicalJSON(files)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// VerifyChecksum 校验文件集合是否匹配声明的 checksum。
func VerifyChecksum(files []File, want string) (bool, error) {
	got, err := Checksum(files)
	if err != nil {
		return false, err
	}
	return got == want, nil
}

// ShortSum 取 checksum 的前 16 位十六进制，作为 store 中的内容目录名。
//
// store 按内容而非版本号分目录（技术方案 §8.0）：回滚是「用旧内容发布新版本」，
// 因此新旧版本的 checksum 必然相同，两者命中同一目录，回滚零 IO。
func ShortSum(checksum string) string {
	hexPart := strings.TrimPrefix(checksum, "sha256:")
	if len(hexPart) < 16 {
		return ""
	}
	return hexPart[:16]
}
