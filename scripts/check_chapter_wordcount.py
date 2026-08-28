#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
Kiem tra so tu chuong
Kiem tra so tu cua file chuong chi dinh, duoi 2000 tu thi canh bao can mo rong.
"""

import re
import sys
from pathlib import Path

if sys.platform == 'win32':
    import io
    sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8', errors='replace')
    sys.stderr = io.TextIOWrapper(sys.stderr.buffer, encoding='utf-8', errors='replace')


def count_words(text: str) -> int:
    """Dem so tu (bao gom tieng Viet va CJK)."""
    text = re.sub(r'#{1,6}\s*', '', text)
    text = re.sub(r'\*\*(.*?)\*\*', r'\1', text)
    text = re.sub(r'\*(.*?)\*', r'\1', text)
    text = re.sub(r'~~(.*?)~~', r'\1', text)
    text = re.sub(r'`(.*?)`', r'\1', text)
    text = re.sub(r'\[(.*?)\]\(.*?\)', r'\1', text)

    total = 0
    for token in text.split():
        cjk = sum(1 for c in token if '\u4e00' <= c <= '\u9fff' or '\u3400' <= c <= '\u4dbf')
        if cjk > 0 and all('\u4e00' <= c <= '\u9fff' or '\u3400' <= c <= '\u4dbf' or c.isspace() for c in token):
            total += cjk
        elif any(c.isalpha() for c in token):
            total += 1
    return total


def extract_content_from_chapter(file_path: Path) -> str:
    """Trich xuat phan than chuong (loai tru title va metadata)."""
    content = file_path.read_text(encoding='utf-8')
    lines = content.split('\n')

    content_start = 0
    for i, line in enumerate(lines):
        if line.startswith('#') and 'chuong' in line.lower():
            content_start = i + 1
            break

    return '\n'.join(lines[content_start:])


def check_chapter(file_path: str, min_words: int = 2000) -> dict:
    """Kiem tra so tu cua mot chuong."""
    path = Path(file_path)
    if not path.exists():
        return {
            'file': str(path),
            'exists': False,
            'word_count': 0,
            'status': 'error',
            'message': f'File khong ton tai: {file_path}',
        }

    main_content = extract_content_from_chapter(path)
    word_count = count_words(main_content)
    status = 'pass' if word_count >= min_words else 'fail'
    message = f'So tu: {word_count}'
    if word_count >= min_words:
        message += ' (dat)'
    else:
        message += f' (thieu, can it nhat {min_words} tu)'

    return {
        'file': str(path),
        'exists': True,
        'word_count': word_count,
        'status': status,
        'message': message,
    }


def check_all_chapters(directory: str, pattern: str = 'chuong*.md', min_words: int = 2000) -> list:
    """Kiem tra tat ca chapter files trong thu muc."""
    dir_path = Path(directory)
    if not dir_path.exists():
        print(f'Loi: thu muc khong ton tai — {directory}')
        return []

    chapter_files = sorted(dir_path.glob(pattern))
    return [check_chapter(str(chapter_file), min_words) for chapter_file in chapter_files]


def print_results(results: list, min_words: int = 2000) -> None:
    """In ket qua kiem tra."""
    if not results:
        print('Khong tim thay file chuong nao')
        return

    total_words = 0
    passed = 0
    failed = 0

    print('\n' + '=' * 60)
    print('Bao cao kiem tra so tu chuong')
    print('=' * 60)

    for result in results:
        if not result['exists']:
            print(f'\nKhong thay {result["file"]}')
            print(f'   {result["message"]}')
            continue

        total_words += result['word_count']
        if result['status'] == 'pass':
            passed += 1
            icon = 'OK'
        else:
            failed += 1
            icon = 'THIEU'

        print(f'\n{icon} {Path(result["file"]).name}')
        print(f'   {result["message"]}')

    print('\n' + '-' * 60)
    print(f'Tong: {len(results)} chuong | {passed} dat | {failed} thieu | Tong so tu: {total_words:,}')
    print('-' * 60)

    if failed > 0:
        print(f'\nCo {failed} chuong khong du {min_words} tu, kien nghi dung ky thuat mo rong:')
        print('   — Them chi tiet mo ta (moi truong, tam ly, hanh dong)')
        print('   — Tang cuong doan hoi thoai')
        print('   — Mo rong hoat dong noi tam nhan vat')
        print('   — Bo sung cau chuyen nen')
        print('\n   Tham khao: references/content-expansion.md')


def main() -> None:
    """Ham chinh."""
    if len(sys.argv) < 2:
        print('Cach dung:')
        print('  Kiem tra mot chuong: python check_chapter_wordcount.py <duong-dan-file-chuong> [so-toi-thieu]')
        print('  Kiem tra tat ca:      python check_chapter_wordcount.py --all <duong-dan-thu-muc> [so-toi-thieu]')
        print('')
        print('Vi du:')
        print('  python check_chapter_wordcount.py novels/truyen/chuong01.md')
        print('  python check_chapter_wordcount.py novels/truyen/chuong01.md 2500')
        print('  python check_chapter_wordcount.py --all novels/truyen')
        print('  python check_chapter_wordcount.py --all novels/truyen 2500')
        return

    if sys.argv[1] == '--all':
        if len(sys.argv) < 3:
            print('Loi: can chi dinh duong dan thu muc khi dung --all')
            return
        directory = sys.argv[2]
        min_words = int(sys.argv[3]) if len(sys.argv) > 3 else 2000
        results = check_all_chapters(directory, min_words=min_words)
        print_results(results, min_words)
        return

    file_path = sys.argv[1]
    min_words = int(sys.argv[2]) if len(sys.argv) > 2 else 2000
    result = check_chapter(file_path, min_words)
    print_results([result], min_words)


if __name__ == '__main__':
    main()
