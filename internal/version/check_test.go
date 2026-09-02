package version

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func httpResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func releasePayload(tag, body string) string {
	b, err := json.Marshal(map[string]any{"tag_name": tag, "body": body, "assets": []any{}})
	if err != nil {
		panic(err)
	}
	return string(b)
}

func TestIsNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
		wantErr         bool
	}{
		{latest: "v1.2.4", current: "v1.2.3", want: true},
		{latest: "v1.2.3", current: "v1.2.3"},
		{latest: "v1.2.2", current: "v1.2.3"},
		{latest: "v1.3.0", current: "v1.2.9", want: true},
		{latest: "v2.0.0", current: "v1.9.9", want: true},
		{latest: "1.2.4", current: "v1.2.3", want: true},
		{latest: "v1.2.3-rc.1", current: "v1.2.2", want: true},
		{latest: "v1.2.3", current: "v1.2.3-rc.1", want: true},
		{latest: "v1.2.3+build.2", current: "v1.2.3+build.1"},
		{latest: "nightly", current: "v1.0.0", wantErr: true},
		{latest: "", current: "v1.0.0", wantErr: true},
		{latest: "v1.2.4", current: "dev", wantErr: true},
	}
	for _, c := range cases {
		got, err := isNewer(c.latest, c.current)
		if (err != nil) != c.wantErr {
			t.Fatalf("isNewer(%q, %q) error = %v, wantErr %v", c.latest, c.current, err, c.wantErr)
		}
		if got != c.want {
			t.Fatalf("isNewer(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}

func TestCheckUpdateDevSkipsNetwork(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return httpResp(200, releasePayload("v9.9.9", "notes")), nil
	})}
	res, err := CheckUpdate(context.Background(), CheckOptions{
		CurrentVersion: "dev",
		Client:         client,
	})
	if err != nil {
		t.Fatalf("CheckUpdate: %v", err)
	}
	if res.UpdateAvailable || calls != 0 {
		t.Fatalf("bản build dev phải bỏ qua kiểm tra: res=%+v calls=%d", res, calls)
	}
}

func TestCheckUpdateFetchesThenCaches(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return httpResp(200, releasePayload("v1.2.4", "## New Features\n* feat: x")), nil
	})}
	cachePath := filepath.Join(t.TempDir(), "update-check.json")
	opts := CheckOptions{CurrentVersion: "v1.2.3", Client: client, CachePath: cachePath}

	res, err := CheckUpdate(context.Background(), opts)
	if err != nil {
		t.Fatalf("CheckUpdate lần đầu: %v", err)
	}
	if !res.UpdateAvailable || res.Latest != "v1.2.4" || res.FromCache {
		t.Fatalf("kết quả lần đầu = %+v", res)
	}
	if res.Notes == "" {
		t.Fatalf("ghi chú phát hành phải được trả về cùng kết quả")
	}
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("cache chưa được ghi xuống đĩa: %v", err)
	}

	res2, err := CheckUpdate(context.Background(), opts)
	if err != nil {
		t.Fatalf("CheckUpdate lần hai: %v", err)
	}
	if !res2.FromCache {
		t.Fatalf("lần hai phải dùng cache: %+v", res2)
	}
	if calls != 1 {
		t.Fatalf("tiết lưu cache thất bại: liên mạng %d lần, muốn 1", calls)
	}
}

func TestCheckUpdateCacheExpiry(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return httpResp(200, releasePayload("v1.2.4", "")), nil
	})}
	cachePath := filepath.Join(t.TempDir(), "update-check.json")
	// Cache đã quá thời hạn MaxAge phải được coi là hết hạn và kiểm tra lại qua mạng.
	stale := checkCache{LastCheck: time.Now().Add(-2 * time.Hour), Latest: "v1.2.4"}
	if err := os.WriteFile(cachePath, mustJSON(stale), 0o644); err != nil {
		t.Fatalf("ghi cache cũ: %v", err)
	}
	_, err := CheckUpdate(context.Background(), CheckOptions{
		CurrentVersion: "v1.2.3", Client: client, CachePath: cachePath, MaxAge: time.Hour,
	})
	if err != nil {
		t.Fatalf("CheckUpdate: %v", err)
	}
	if calls != 1 {
		t.Fatalf("cache hết hạn phải kích hoạt liên mạng: calls=%d", calls)
	}
}

func TestCheckUpdateDoesNotFallbackToStaleCache(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return httpResp(500, "rate limited"), nil
	})}
	cachePath := filepath.Join(t.TempDir(), "update-check.json")
	stale := checkCache{LastCheck: time.Now().Add(-48 * time.Hour), Latest: "v1.2.4", Notes: "old notes"}
	if err := os.WriteFile(cachePath, mustJSON(stale), 0o644); err != nil {
		t.Fatalf("ghi cache cũ: %v", err)
	}
	res, err := CheckUpdate(context.Background(), CheckOptions{
		CurrentVersion: "v1.2.3", Client: client, CachePath: cachePath,
	})
	if err == nil {
		t.Fatal("lỗi mạng không nên bị cache hết hạn che giấu")
	}
	if res != nil {
		t.Fatalf("khi thất bại không nên trả kết quả hết hạn: %+v", res)
	}
	if calls != 1 {
		t.Fatalf("sau cache hết hạn phải thử liên mạng một lần: calls=%d", calls)
	}
}

func TestCheckUpdateRepairsCorruptCacheAndReportsIt(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return httpResp(200, releasePayload("v1.2.4", "notes")), nil
	})}
	cachePath := filepath.Join(t.TempDir(), "update-check.json")
	if err := os.WriteFile(cachePath, []byte(`{broken`), 0o600); err != nil {
		t.Fatalf("ghi cache hỏng: %v", err)
	}
	opts := CheckOptions{CurrentVersion: "v1.2.3", Client: client, CachePath: cachePath}

	res, err := CheckUpdate(context.Background(), opts)
	if res == nil || !res.UpdateAvailable {
		t.Fatalf("sau khi yêu cầu lại phải trả kết quả hợp lệ: %+v", res)
	}
	if err == nil || !strings.Contains(err.Error(), "đọc cache kiểm tra cập nhật") {
		t.Fatalf("cache hỏng phải trả lỗi đồng hành, nhận được: %v", err)
	}

	res, err = CheckUpdate(context.Background(), opts)
	if err != nil || res == nil || !res.FromCache {
		t.Fatalf("cache đã sửa phải dùng được trực tiếp: res=%+v err=%v", res, err)
	}
	if calls != 1 {
		t.Fatalf("cache hợp lệ không nên liên mạng lại: calls=%d", calls)
	}
}

func TestCheckUpdateReturnsResultWithCacheWriteError(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return httpResp(200, releasePayload("v1.2.4", "notes")), nil
	})}
	parentFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(parentFile, []byte("x"), 0o600); err != nil {
		t.Fatalf("ghi file cha: %v", err)
	}

	res, err := CheckUpdate(context.Background(), CheckOptions{
		CurrentVersion: "v1.2.3",
		Client:         client,
		CachePath:      filepath.Join(parentFile, "update-check.json"),
	})
	if res == nil || !res.UpdateAvailable {
		t.Fatalf("lỗi ghi cache không nên làm mất kết quả liên mạng: %+v", res)
	}
	if err == nil || !strings.Contains(err.Error(), "ghi cache kiểm tra cập nhật") {
		t.Fatalf("lỗi ghi cache phải trả lỗi đồng hành, nhận được: %v", err)
	}
}

func TestCheckUpdateErrorWithoutCache(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return httpResp(500, "boom"), nil
	})}
	_, err := CheckUpdate(context.Background(), CheckOptions{
		CurrentVersion: "v1.2.3",
		Client:         client,
		CachePath:      filepath.Join(t.TempDir(), "absent.json"),
	})
	if err == nil {
		t.Fatalf("không có cache và mạng lỗi phải trả error để caller ghi log")
	}
}

func TestCheckUpdateMissingTag(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return httpResp(200, `{"assets": []}`), nil
	})}
	_, err := CheckUpdate(context.Background(), CheckOptions{
		CurrentVersion: "v1.2.3",
		Client:         client,
		CachePath:      filepath.Join(t.TempDir(), "absent.json"),
	})
	if err == nil {
		t.Fatalf("release thiếu tag_name phải báo lỗi")
	}
}

func TestCheckUpdateInvalidTag(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return httpResp(200, releasePayload("nightly", "")), nil
	})}
	_, err := CheckUpdate(context.Background(), CheckOptions{
		CurrentVersion: "v1.2.3",
		Client:         client,
	})
	if err == nil || !strings.Contains(err.Error(), "phiên bản mới nhất không hợp lệ") {
		t.Fatalf("tag release không hợp lệ phải báo lỗi, nhận được: %v", err)
	}
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
