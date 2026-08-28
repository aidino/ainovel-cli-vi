package imp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDigestStableAndDistinct(t *testing.T) {
	a := Digest([]byte("第一chương "))
	if a != Digest([]byte("第一chương ")) {
		t.Fatal("同输入 digest 不稳定")
	}
	if a == Digest([]byte("第二chương ")) {
		t.Fatal("khác nhau 输入 digest giống nhau ")
	}
	if len(a) < 8 || a[:7] != "sha256:" {
		t.Fatalf("digest tiền tố không khớp ：%s", a)
	}
}

func TestWorkspaceAtomicRoundtrip(t *testing.T) {
	w := &Workspace{dir: t.TempDir()}
	if err := w.writeAtomic("nested/x.txt", []byte("hello")); err != nil {
		t.Fatalf("writeAtomic: %v", err)
	}
	got, err := os.ReadFile(w.path("nested/x.txt"))
	if err != nil || string(got) != "hello" {
		t.Fatalf("读回không khớp ：%q %v", got, err)
	}
}

func TestArtifactRoundtripPreservesIdentity(t *testing.T) {
	w := &Workspace{dir: t.TempDir()}
	type payload struct {
		N int `json:"n"`
	}
	if err := writeArtifact(w, "seg.json", "sha256:abc", payload{N: 7}); err != nil {
		t.Fatalf("writeArtifact: %v", err)
	}
	a, err := readArtifact[payload](w, "seg.json")
	if err != nil {
		t.Fatalf("readArtifact: %v", err)
	}
	if a.InputDigest != "sha256:abc" || a.Payload.N != 7 || a.SchemaVersion != workspaceSchemaVersion {
		t.Fatalf("身份未giữ lại ：%+v", a)
	}
}

func TestReadArtifactRejectsSchemaMismatch(t *testing.T) {
	w := &Workspace{dir: t.TempDir()}
	// 直接写一个 schema phiên bản không khớp 的工件。
	raw := Artifact[string]{SchemaVersion: 999, InputDigest: "sha256:x", Payload: "y"}
	if err := w.writeJSON("seg.json", raw); err != nil {
		t.Fatal(err)
	}
	if _, err := readArtifact[string](w, "seg.json"); err == nil {
		t.Fatal("schema phiên bản không khớp nên bị từ chối绝")
	}
}

func TestCreateWorkspacePublishesAtomically(t *testing.T) {
	book := t.TempDir()
	norm := []byte("第一chương \nchính văn\n")
	m := Manifest{
		Version:          workspaceSchemaVersion,
		SourceName:       "book.txt",
		NormalizedSHA256: Digest(norm),
		Encoding:         encodingUTF8,
	}
	ws, err := createWorkspace(book, m, Intent{Version: workspaceSchemaVersion}, norm)
	if err != nil {
		t.Fatalf("createWorkspace: %v", err)
	}
	if !ws.Active() {
		t.Fatal("发布后工作区nên là活动")
	}
	for _, f := range []string{fileManifest, fileIntent, fileSource} {
		if !ws.has(f) {
			t.Fatalf("缺工件 %s", f)
		}
	}
	// createWorkspace thành công后不应泄漏半khởi tạo 临时thư mục（meta/import.init-*）。
	if dirs, _ := filepath.Glob(filepath.Join(book, "meta", "import.init-*")); len(dirs) != 0 {
		t.Fatalf("发布thành công后不应残留 init thư mục：%v", dirs)
	}
	// lặp lại 创建应因已存在而thất bại。
	if _, err := createWorkspace(book, m, Intent{}, norm); err == nil {
		t.Fatal("已存在活动工作区时lặp lại 创建应thất bại")
	}
}

func TestCreateWorkspaceRejectsInconsistentSnapshot(t *testing.T) {
	book := t.TempDir()
	m := Manifest{Version: workspaceSchemaVersion, NormalizedSHA256: Digest([]byte("A"))}
	// manifest 声明的摘要与thực tế ghi 的 normalized không nhất quán → 发布前校验应chặn。
	if _, err := createWorkspace(book, m, Intent{}, []byte("B")); err == nil {
		t.Fatal("源快照与 manifest 摘要không nhất quán时应từ chối发布")
	}
	if _, err := os.Stat(filepath.Join(book, "meta", "import")); !os.IsNotExist(err) {
		t.Fatal("发布thất bại后不应留下活动工作区")
	}
}
