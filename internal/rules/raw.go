package rules

import (
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RawSource là một nguồn gốc chờ chuẩn hóa (toàn bộ đoạn văn bản của file rules).
//
// Sau khi bỏ YAML, file rules chỉ là prompt ngôn ngữ tự nhiên thông thường; chuẩn hóa chỉ cần nguyên văn, không còn phân tích front matter.
type RawSource struct {
	Label string     // Nhãn nguồn, đi vào Snapshot.Sources (ví dụ global:my-style.md)
	Kind  SourceKind // Cấp độ ưu tiên
	Text  string     // Nội dung gốc của file
}

// RawFileSources liệt kê các file .md trong thư mục rules theo thứ tự Global → Project và trả về văn bản gốc.
//
// Cùng quy ước quét với readDirFromDisk (chỉ .md ở cấp cao nhất, thứ tự từ điển, bỏ qua file ẩn), nhưng không phân tích YAML,
// toàn bộ đoạn văn bản được giao nguyên trạng cho bộ chuẩn hóa. System defaults / prompt khởi động / yêu cầu lúc chạy do service cung cấp riêng.
func RawFileSources(opts LoadOptions) []RawSource {
	var out []RawSource
	out = append(out, rawDir(opts.HomeRulesDir, SourceGlobal)...)
	out = append(out, rawDir(opts.ProjectRulesDir, SourceProject)...)
	return out
}

func rawDir(dir string, kind SourceKind) []RawSource {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		// Thư mục không tồn tại là chuyện thường, âm thầm bỏ qua; nhưng lỗi như quyền hạn/đường dẫn thực chất là file thì phải lưu vết ——
		// nếu không người dùng viết quy tắc nhưng hoàn toàn không có hiệu lực, phản hồi bằng 0, chi phí rà soát cực cao (xem known_rules_path_stale_readme).
		if !os.IsNotExist(err) {
			slog.Warn("đọc thư mục rules thất bại, đã bỏ qua", "module", "rules", "dir", dir, "err", err)
		}
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") || !strings.EqualFold(filepath.Ext(e.Name()), ".md") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	var out []RawSource
	for _, name := range names {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			slog.Warn("đọc file rules thất bại, đã bỏ qua", "module", "rules", "file", path, "err", err)
			continue
		}
		text := strings.TrimSpace(string(data))
		if text == "" {
			continue
		}
		out = append(out, RawSource{
			Label: kind.String() + ":" + name,
			Kind:  kind,
			Text:  text,
		})
	}
	return out
}