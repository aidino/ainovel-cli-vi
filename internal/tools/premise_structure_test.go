package tools

import (
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
)

func TestParsePremiseSections(t *testing.T) {
	premise := `# Premise

## Thể loại và tông giọng
东方玄幻，冷硬成长。

## Định vị thể loại
东方玄幻升级流，面向追求爽点和关系推进的读者。

## Xung đột cốt lõi
主角必须在宗门规则与个人良知之间做选择。

## Bước ngoặt giữa truyện
旧有修炼路线失效，必须转向禁术体系。
`

	sections := parsePremiseSections(premise)
	if sections["Thể loại và tông giọng"] == "" {
		t.Fatalf("expected 'Thể loại và tông giọng' section (từ heading gốc 题材和基调), got %+v", sections)
	}
	if sections["Định vị thể loại"] == "" {
		t.Fatalf("expected 'Định vị thể loại' section (từ heading gốc 题材定位), got %+v", sections)
	}
	if sections["Xung đột cốt lõi"] == "" {
		t.Fatalf("expected 'Xung đột cốt lõi' section (từ heading gốc 核心冲突), got %+v", sections)
	}
	if sections["Bước ngoặt giữa truyện"] == "" {
		t.Fatalf("expected alias 中期转向 normalized to 'Bước ngoặt giữa truyện', got %+v", sections)
	}
}

func TestPremiseStructure(t *testing.T) {
	premise := `## Thể loại và tông giọng
升级流，偏冷硬。

## Định vị thể loại
升级流

## Xung đột cốt lõi
冲突

## Mục tiêu nhân vật chính
目标

## Hướng kết thúc
终局

## Vùng cấm khi viết
禁区

## Điểm mạnh khác biệt
卖点

## Móc khác biệt
钩子

## Lời hứa cốt lõi
兑现

## Động cơ truyện
引擎

## Bước ngoặt giữa truyện
转折
`

	structure := premiseStructure(premise, domain.PlanningTierMid)
	if ready, _ := structure["template_ready"].(bool); !ready {
		t.Fatalf("expected template_ready, got %+v", structure)
	}
	missing, _ := structure["missing"].([]string)
	if len(missing) != 0 {
		t.Fatalf("expected no missing headings, got %+v", missing)
	}
}

func TestPremiseStructureShortAcceptsLegacyHeadingAlias(t *testing.T) {
	premise := `## Thể loại và tông giọng
单卷高压营救。

## Định vị thể loại
短篇高密度冒险。

## Xung đột cốt lõi
主角必须在一夜内救出人质。

## Mục tiêu nhân vật chính
救出人质并活着离开。

## Hướng kết thúc
完成任务但付出代价。

## Vùng cấm khi viết
不扩展成长期连载。

## Điểm mạnh khác biệt
时限压力与连续反转。

## Móc khác biệt
每次选择都缩短救援时间。

## Lời hứa cốt lõi
紧迫感、抉择与反转。

## Độ phù hợp dạng ngắn
核心矛盾和人物弧线都能在单次任务中完成。
`

	structure := premiseStructure(premise, domain.PlanningTierShort)
	if ready, _ := structure["template_ready"].(bool); !ready {
		t.Fatalf("expected short template_ready, got %+v", structure)
	}
}
