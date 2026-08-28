// Package stylestat thống kê văn phong cấp toàn thư trên phần thân đã viết, sinh thuần dữ kiện.
//
// Động cơ: cửa sổ đọc kiểm trong arc (~10 chương) vốn mù với sự cố hữu khuôn mẫu cấp toàn thư —
// tic câu thức trung bình vài chục lần mỗi chương, hình thái cuối chương đồng dạng, đọc lặp xuyên
// chương; nhìn từng chương thì chỗ nào cũng "bình thường", chỉ thống kê toàn thư mới lộ ra.
// Thống kê thuộc về code (tất định, không ảo giác), phán quyết thuộc về LLM (editor chấm điểm
// phạm trù theo con số, writer dựa vào đó để tự tránh). Compute phục vụ đánh giá offline tính
// toàn lượng một lần; runtime dùng Tracker bảo trì tăng dần theo từng chương.
// Đã bản địa hóa cho văn bản tiếng Việt: tách câu, từ thời gian, khuôn câu AI, khai thác cụm từ.
package stylestat

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// minChapters dưới số chương này thì không ra thống kê — mẫu quá nhỏ, tần suất vô nghĩa.
const minChapters = 5

// phraseWindow khai thác cụm từ động chỉ nhìn N chương gần nhất: cái writer cần tránh là "khuôn câu quen miệng hiện tại".
const phraseWindow = 20

// Input đầu vào thống kê. Chapters theo số chương tăng dần; Stopwords là tên riêng
// như tên nhân vật, bỏ qua khi khai thác cụm từ động (tên nhân vật xuất hiện vốn
// tần suất cao tự nhiên, không phải vấn đề văn phong).
type Input struct {
	Chapters  []string
	Titles    []string
	Stopwords []string
}

// Stats kết quả thống kê văn phong toàn thư. Mọi trường đều là dữ kiện đếm được, không chứa phán quyết hay chỉ lệnh.
type Stats struct {
	Chapters          int            `json:"chapters"`
	Patterns          []PatternStat  `json:"patterns,omitempty"`
	TopPhrases        []PhraseStat   `json:"top_phrases,omitempty"`
	RepeatedSentences []SentenceStat `json:"repeated_sentences,omitempty"`
	Ending            EndingStat     `json:"ending"`
	OpeningTimeRate   float64        `json:"opening_time_rate"`
	TitleFormats      *TitleStat     `json:"title_formats,omitempty"`
}

// PatternStat đếm cấp toàn thư của một lớp khuôn câu cố định (tic văn phong AI thông dụng).
type PatternStat struct {
	Name       string  `json:"name"`
	Total      int     `json:"total"`
	PerChapter float64 `json:"per_chapter"`
}

// PhraseStat cụm từ tần suất cao khai thác được trong phraseWindow chương gần nhất.
type PhraseStat struct {
	Text  string `json:"text"`
	Count int    `json:"count"`
}

// SentenceStat câu dài lặp nguyên văn xuyên chương (bằng chứng trực tiếp của việc nhắc lại tình tiết).
type SentenceStat struct {
	Text     string `json:"text"`
	Chapters int    `json:"chapters"`
	Count    int    `json:"count"`
}

// EndingStat phân bố hình thái dòng cuối chương. Kết thúc ngắn vốn hợp pháp; đồng dạng toàn thư mới là vấn đề.
type EndingStat struct {
	ShortRatio  float64 `json:"short_ratio"`
	MedianRunes int     `json:"median_runes"`
}

// TitleStat đếm trộn lẫn tiền tố "Chương N" trong tiêu đề chương (trộn lẫn = dấu vết cơ chế lộ ra sản phẩm).
type TitleStat struct {
	WithPrefix    int `json:"with_prefix"`
	WithoutPrefix int `json:"without_prefix"`
}

// patternDefs các khuôn câu tic văn phong AI thông dụng cho văn bản tiếng Việt.
// Số đếm là xấp xỉ (regex không phân tích ngữ pháp), mục đích là so baseline dọc
// của chính bộ sách, độ chính xác tuyệt đối không quan trọng.
var patternDefs = []struct {
	name string
	re   *regexp.Regexp
}{
	{"Câu chỉnh đính 'không phải… mà là…'", regexp.MustCompile(`(?i)không\s+phải[^.!?\n]{1,60}?mà\s+là`)},
	{"Từ nhịp thời gian 'X nhịp hơi'", regexp.MustCompile(`(?i)(?:mấy|một|vài|nửa|ba|hai)\s+nhịp(?:\s+hơi)?`)},
	{"Ẩn dụ mở 'như thể/tựa như/như một'", regexp.MustCompile(`(?i)như thể|tựa như|tựa hồ|như một|như là`)},
	{"Nhịp im lặng 'im lặng/không nói gì'", regexp.MustCompile(`(?i)im lặng|không nói gì|không đáp lời|không quay đầu`)},
	{"Khuôn mẫu thần thái 'lóe lên/khóe môi nhếch'", regexp.MustCompile(`(?i)lóe lên|đồng tử co|khóe môi (?:nhếch|cong lên|giật)|cắn (?:chặt )?môi|không thể tin (?:nổi|được)|sững (?:người|lại)|ngẩn ra`)},
	{"Phản ứng thân thể 'tim thắt/người run/rùng mình'", regexp.MustCompile(`(?i)tim (?:thắt|đập thình thịch)|lòng (?:thắt|chùng xuống)|người run (?:lên|ráy)|lưng lạnh(?: buốt)?|hít (?:một hơi|vào) (?:không khí )?lạnh|rùng mình`)},
	{"Dấu hiệu tư duy 'trong đầu nghĩ/cảm thấy/ý thức được'", regexp.MustCompile(`(?i)trong đầu nghĩ|ý thức được|cảm thấy|nghĩ rằng|đoán rằng|trong lòng nghĩ`)},
	{"Khuôn sáo trừu tượng 'một cảm giác khó tả/điều quan trọng là'", regexp.MustCompile(`(?i)một cảm giác (?:khó tả|khó nói)|nói không nên lời|ý nghĩa của|điều quan trọng là|thực sự quan trọng`)},
}

var (
	// Tách câu tiếng Việt: dấu chấm, chấm hỏi, chấm than, chấm lửng, xuống dòng.
	sentenceSplit = regexp.MustCompile(`[.!?…\n]+`)
	// Từ thời gian mở đầu chương (tương đương các từ mở đầu thời gian của bản gốc).
	openingTimeRe = regexp.MustCompile(`(?i)(?:suốt đêm|đêm khuya|đêm xuống|sáng sớm|rạng đông|trời vừa sáng|trời sáng|tỉnh dậy|thức dậy|ánh nắng sớm)`)
	// Tiền tố "Chương N" trong tiêu đề (hỗ trợ số Ả Rập và chữ, hoa thường).
	titlePrefixRe = regexp.MustCompile(`(?i)^#{0,2}\s*chương\s+\S+`)
)

// shortEndingRunes dòng cuối không vượt quá số ký tự này thì tính là "kết thúc ngắn"
// (quy đổi từ 30 chữ Hán của bản gốc sang phạm vi tiếng Việt).
const shortEndingRunes = 60

// minRepeatRunes câu lặp nguyên văn phải đạt tối thiểu số ký tự này (quy đổi từ 12 chữ Hán).
const minRepeatRunes = 40

// repeatDisplayRunes độ dài hiển thị tối đa của câu lặp trong báo cáo (dùng chung Compute/Tracker).
const repeatDisplayRunes = 80

// Compute tính thống kê văn phong toàn thư; không đủ số chương thì trả nil.
func Compute(in Input) *Stats {
	n := len(in.Chapters)
	if n < minChapters {
		return nil
	}
	all := strings.Join(in.Chapters, "\n")

	s := &Stats{Chapters: n}
	for _, def := range patternDefs {
		total := len(def.re.FindAllStringIndex(all, -1))
		if total == 0 {
			continue
		}
		s.Patterns = append(s.Patterns, PatternStat{
			Name:       def.name,
			Total:      total,
			PerChapter: round1(float64(total) / float64(n)),
		})
	}
	s.TopPhrases = minePhrases(recentWindow(in.Chapters), in.Stopwords)
	s.RepeatedSentences = repeatedSentences(in.Chapters)
	s.Ending = endingShape(in.Chapters)
	s.OpeningTimeRate = openingTimeRate(in.Chapters)
	s.TitleFormats = titleFormats(in.Titles)
	return s
}

func recentWindow(chapters []string) []string {
	if len(chapters) <= phraseWindow {
		return chapters
	}
	return chapters[len(chapters)-phraseWindow:]
}

// minePhrases khai thác cụm từ tần suất cao (1-2 từ) trong cửa sổ gần nhất, theo từ tiếng Việt.
// Lọc: chứa số/chữ ngắn/hư từ đầu cuối, trúng tên riêng; khử trùng: cụm đã chọn chứa nhau thì bỏ.
func minePhrases(chapters []string, stopwords []string) []PhraseStat {
	text := strings.Join(chapters, "\n")
	threshold := max(8, len(chapters)/2)

	counts := make(map[string]int)
	for _, size := range []int{1, 2} {
		for _, gram := range wordGrams(text, size) {
			counts[gram]++
		}
	}

	stopSet := stopwordGrams(stopwords)
	type cand struct {
		text  string
		count int
	}
	var cands []cand
	for g, c := range counts {
		if c < threshold || hitStopword(g, stopSet) {
			continue
		}
		cands = append(cands, cand{g, c})
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].count != cands[j].count {
			return cands[i].count > cands[j].count
		}
		// Cùng tần suất lấy dài hơn (nhiều thông tin hơn), rồi sắp theo từ điển cho ổn định
		if len(cands[i].text) != len(cands[j].text) {
			return len(cands[i].text) > len(cands[j].text)
		}
		return cands[i].text < cands[j].text
	})

	var out []PhraseStat
	for _, c := range cands {
		if len(out) >= 8 {
			break
		}
		dup := false
		for _, picked := range out {
			if strings.Contains(picked.Text, c.text) || strings.Contains(c.text, picked.Text) {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, PhraseStat{Text: c.text, Count: c.count})
		}
	}
	return out
}

// gramEdgeStopWords hư từ / đại từ ở đầu hoặc cuối cụm — không phải cụm văn phong, bỏ qua.
var gramEdgeStopWords = map[string]bool{
	"của": true, "là": true, "được": true, "có": true, "và": true, "với": true,
	"cũng": true, "đều": true, "lại": true, "còn": true, "nữa": true, "bị": true,
	"bằng": true, "ở": true, "trên": true, "dưới": true, "khi": true, "rồi": true,
	"ra": true, "vào": true, "này": true, "đó": true, "một": true, "những": true,
	"các": true, "đã": true, "sẽ": true, "đang": true, "không": true, "ta": true,
	"tôi": true, "nó": true, "anh": true, "nàng": true, "hắn": true, "y": true,
}

// wordGrams tách văn bản thành các n-gram từ (size từ), chuẩn hóa chữ thường,
// chỉ giữ từ chữ cái thuần và đủ dài; từ đầu/cuối là hư từ thì loại cả cụm.
func wordGrams(text string, size int) []string {
	fields := strings.Fields(text)
	var words []string
	for _, f := range fields {
		w := strings.Trim(f, `"'“”‘’«».,!?;:…—–()-`)
		w = strings.ToLower(w)
		if !isWordy(w) {
			words = append(words, "")
			continue
		}
		words = append(words, w)
	}
	var grams []string
	for i := 0; i+size <= len(words); i++ {
		gram := words[i : i+size]
		if gram[0] == "" || gram[len(gram)-1] == "" {
			continue
		}
		if gramEdgeStopWords[gram[0]] || gramEdgeStopWords[gram[len(gram)-1]] {
			continue
		}
		grams = append(grams, strings.Join(gram, " "))
	}
	return grams
}

// isWordy kiểm tra một token có phải từ nội dung hợp lệ: toàn chữ cái (cho phép dấu),
// dài tối thiểu 3 ký tự (bỏ hư từ ngắn kiểu "là", "và" vốn đã lọc bằng edge-stop).
func isWordy(w string) bool {
	if len([]rune(w)) < 3 {
		return false
	}
	for _, r := range w {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return true
}

// stopwordGrams sinh tập lọc từ tên riêng: tên tiếng Việt thường nhiều từ
// ("Lâm Trần"), vừa lọc nguyên cụm vừa lọc từng từ đơn — thà lọc chặt,
// thiếu một dữ kiện cụm không sao, tên riêng lọt vào danh sách khuôn câu mới là nhiễu.
func stopwordGrams(stopwords []string) map[string]bool {
	set := make(map[string]bool)
	for _, w := range stopwords {
		w = strings.ToLower(strings.TrimSpace(w))
		if w == "" {
			continue
		}
		set[w] = true
		for _, part := range strings.Fields(w) {
			if len([]rune(part)) >= 3 {
				set[part] = true
			}
		}
	}
	return set
}

func hitStopword(gram string, stopSet map[string]bool) bool {
	fields := strings.Fields(strings.ToLower(gram))
	for _, f := range fields {
		if stopSet[f] {
			return true
		}
	}
	return false
}

// repeatedSentences tìm câu ≥ minRepeatRunes ký tự lặp nguyên văn xuyên ≥3 chương, lấy top 5 theo số lần.
func repeatedSentences(chapters []string) []SentenceStat {
	type rec struct {
		count    int
		chapters map[int]struct{}
	}
	seen := make(map[string]*rec)
	for ci, text := range chapters {
		for sent, count := range chapterSentenceCounts(text) {
			r := seen[sent]
			if r == nil {
				r = &rec{chapters: make(map[int]struct{})}
				seen[sent] = r
			}
			r.count += count
			r.chapters[ci] = struct{}{}
		}
	}

	var out []SentenceStat
	for sent, r := range seen {
		if len(r.chapters) < 3 {
			continue
		}
		out = append(out, SentenceStat{Text: truncateRunes(sent, repeatDisplayRunes), Chapters: len(r.chapters), Count: r.count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Text < out[j].Text
	})
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

// trimWrappedQuotes bóc dấu ngoặc bao: cùng một câu thoại có/không dấu mở không nên thành hai câu.
func trimWrappedQuotes(sentence string) string {
	return strings.Trim(strings.TrimSpace(sentence), `"'“”‘’«»`)
}

func endingShape(chapters []string) EndingStat {
	var lengths []int
	short := 0
	for _, text := range chapters {
		line := lastNonEmptyLine(text)
		if line == "" {
			continue
		}
		n := len([]rune(line))
		lengths = append(lengths, n)
		if n <= shortEndingRunes {
			short++
		}
	}
	if len(lengths) == 0 {
		return EndingStat{}
	}
	sort.Ints(lengths)
	return EndingStat{
		ShortRatio:  round2(float64(short) / float64(len(lengths))),
		MedianRunes: lengths[len(lengths)/2],
	}
}

func openingTimeRate(chapters []string) float64 {
	hit := 0
	for _, text := range chapters {
		if openingTimeRe.MatchString(firstParagraph(text)) {
			hit++
		}
	}
	return round2(float64(hit) / float64(len(chapters)))
}

func titleFormats(titles []string) *TitleStat {
	if len(titles) == 0 {
		return nil
	}
	t := &TitleStat{}
	for _, title := range titles {
		if strings.TrimSpace(title) == "" {
			continue
		}
		if titlePrefixRe.MatchString(title) {
			t.WithPrefix++
		} else {
			t.WithoutPrefix++
		}
	}
	// Chỉ trộn lẫn mới đáng lên báo; định dạng thống nhất không phải vấn đề theo nghĩa dữ kiện
	if t.WithPrefix == 0 || t.WithoutPrefix == 0 {
		return nil
	}
	return t
}

func lastNonEmptyLine(text string) string {
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

// firstParagraph lấy dòng đầu tiên khác rỗng và không phải tiêu đề Markdown (dòng đầu file chương thường là # tiêu đề).
func firstParagraph(text string) string {
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return line
	}
	return ""
}

func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }
func round2(f float64) float64 { return float64(int(f*100+0.5)) / 100 }