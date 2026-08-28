package store

import (
	"fmt"
	"os"

	"github.com/voocel/ainovel-cli/internal/domain"
)

// BookStore quản lý thông tin đối ngoại của tác phẩm, meta/book.json là nguồn dữ kiện duy nhất, book.md là phép chiếu có thể đọc.
type BookStore struct{ io *IO }

func NewBookStore(io *IO) *BookStore { return &BookStore{io: io} }

// Load đọc thông tin tác phẩm; trả về nil khi chưa được tạo.
func (s *BookStore) Load() (*domain.BookMetadata, error) {
	var book domain.BookMetadata
	if err := s.io.ReadJSON("meta/book.json", &book); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	book = book.Normalized()
	if err := book.Validate(); err != nil {
		return nil, err
	}
	return &book, nil
}

// Save lưu thông tin tác phẩm đã chuẩn hóa và phép chiếu có thể đọc của nó.
func (s *BookStore) Save(book domain.BookMetadata) error {
	book = book.Normalized()
	if err := book.Validate(); err != nil {
		return err
	}
	return s.io.WithWriteLock(func() error {
		if err := s.io.WriteJSONUnlocked("meta/book.json", book); err != nil {
			return err
		}
		return s.io.WriteMarkdownUnlocked("book.md", renderBook(book))
	})
}

func renderBook(book domain.BookMetadata) string {
	return fmt.Sprintf("# %s\n\n## Tóm tắt\n\n%s\n", book.Title, book.Synopsis)
}