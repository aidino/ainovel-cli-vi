# Bảng thuật ngữ Việt hóa (bắt buộc dùng xuyên suốt)

> Mọi bản dịch trong project PHẢI dùng đúng các thuật ngữ dưới đây. Gặp thuật ngữ mới chưa có trong bảng: thêm vào đây trước, rồi mới dịch.

## 1. Cấu trúc tác phẩm

| Tiếng Trung | Tiếng Việt | Ghi chú |
|---|---|---|
| 章 / 章节 | chương | |
| 卷 | tập | |
| 弧 / 卷弧 | arc | giữ nguyên tiếng Anh — thuật ngữ chuyên ngành kết cấu truyện |
| 大纲 | đại cương | outline |
| 前提 | tiền đề | premise |
| 简介 | tóm tắt | synopsis |
| 书名 | tên sách | |
| 世界观 / 世界规则 | thế giới quan / luật thế giới | |
| 角色档案 | hồ sơ nhân vật | |
| 主角 | nhân vật chính | |
| 配角 | nhân vật phụ | |
| 角色状态 | trạng thái nhân vật | |
| 情绪弧线 | cung bậc cảm xúc | |
| 节奏 | nhịp độ | pacing |
| 结局 | kết thúc / cái kết | |

## 2. Kỹ thuật kể chuyện

| Tiếng Trung | Tiếng Việt | Ghi chú |
|---|---|---|
| 伏笔 | chi tiết gieo mầm | foreshadowing — KHÔNG dịch "trở ngại" |
| 埋伏笔 | gieo mầm | |
| 回收伏笔 | thu hoạch mầm | payoff |
| 钩子 | móc | hook |
| 悬念 | hồi hộp / độ treo | suspense |
| 转折 | bước ngoặt | |
| 冲突 | xung đột | |
| 打斗 / 动作场面 | cảnh hành động | |
| 对话 | hội thoại | |
| 独白 | độc thoại | |
| 白描 | tả bạch | |
| 张力 | độ căng | tension |
| 高潮 | cao trào | |
| 收束 / 收尾 | khép lại / đoạn kết | |
| 视角 | góc nhìn | POV |
| 第一人称 / 第三人称 | ngôi thứ nhất / ngôi thứ ba | |

## 3. Quy trình engine (giữ nguyên từ tiếng Anh đã là tiếng Anh)

| Tiếng Trung | Tiếng Việt | Ghi chú |
|---|---|---|
| Engine / Route / Arbiter / Worker / Store | giữ nguyên | danh từ riêng kiến trúc |
| 智能体 | agent | |
| 规划师 | planner | |
| 写手 | writer | |
| 审阅 / 评审 | đọc kiểm / kiểm | review — thống nhất: Editor "đọc kiểm" |
| 裁定 | phán quyết | arbitration verdict |
| 分诊 | phân loại | triage |
| 干预 | can thiệp | user intervention |
| 返工 | làm lại | rework |
| 打磨 | đánh bóng | polish |
| 重写 | viết lại | rewrite |
| 草稿 | bản thảo | draft |
| 正文 | phần thân / thân truyện | |
| 章节契约 | hợp đồng chương | chapter contract |
| 检查点 / 断点恢复 | điểm khôi phục / khôi phục tại điểm dừng | checkpoint |
| 逐章验收 | duyệt từng chương | |
| 许可 | giấy phép | permit |
| 上下文 | ngữ cảnh | context |
| 摘要 | tóm tắt | summary (tùy ngữ cảnh; với tóm tắt sách dùng "tóm tắt") |
| 前情 | tình tiết trước | recap |
| 相关章节推荐 | gợi ý chương liên quan | |
| 滑窗 | cửa sổ trượt | sliding window |
| 分层摘要 | tóm tắt phân tầng | |

## 4. Chất lượng & vận hành

| Tiếng Trung | Tiếng Việt | Ghi chú |
|---|---|---|
| 一致性 | tính nhất quán | |
| 硬伤 | lỗi cứng | fatal inconsistency |
| 节奏拖沓 | nhịp độ lê thê | |
| 字数 | số từ | từ GĐ2: đếm TỪ tiếng Việt, không đếm ký tự |
| 失败出路 | lối thoát thất bại | failure path |
| 僵局 | thế bí | deadlock |
| 回放 | phát lại | replay |
| 审计 | kiểm toán | |
| 落盘 | ghi xuống đĩa | persist |
| 幂等 | idempotent | |
| 无界面 / Headless | headless | giữ nguyên "headless" |
| 断点 | điểm dừng | |

## 5. Quy tắc "cấm dịch"

- Tên tool LLM: `novel_context`, `read_chapter`, `plan_chapter`, `draft_chapter`, `edit_chapter`, `check_consistency`, `commit_chapter`, `save_book`, `save_foundation`, `save_review`, `save_arc_summary`, `save_volume_summary`… → **giữ nguyên**
- Tên field JSON/contract: `working_memory`, `episodic_memory`, `reference_pack`, `memory_policy`, `chapter_plan`, `chapter_contract`, `required_beats`, `forbidden_moves`, `continuity_checks`, `emotion_target`, `payoff_points`, `hook_goal`, `previous_tail`, `related_chapters`, `recent_summaries`… → **giữ nguyên**
- Giá trị enum/JSON persist trên đĩa, tên checkpoint (`chapter:1:plan`), tên file, CLI flags → **giữ nguyên**
- Danh từ riêng kiến trúc: Engine, Route, Arbiter, Worker, Store, Saga, TUI → **giữ nguyên**

## 6. Văn phong

- Xưng hô trong prompt LLM: dùng "bạn" cho agent; giêng mệnh lệnh trực tiếp ("hãy", "phải", "không được").
- Output tiểu thuyết: ALWAYS thêm chỉ thị rõ ràng trong prompt hệ thống: văn bản sáng tác bằng **tiếng Việt**.
- Prompt import (`import-*.md`): phần hướng dẫn viết tiếng Việt, nhưng phải nói rõ agent xử lý **văn bản nguồn ngôn ngữ bất kỳ**.
