package acknowledgment

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func setupAckTestConfig(t *testing.T) {
	t.Helper()
	baseDir, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatalf("解析仓库 server 目录失败: %v", err)
	}
	mapping := filepath.Join(baseDir, "configs", "global_mappings.yaml")
	if _, statErr := os.Stat(mapping); statErr != nil {
		t.Fatalf("找不到 global_mappings.yaml: %v", statErr)
	}
	t.Setenv("PTNEXUS_BASE_DIR", baseDir)
	t.Setenv("PTNEXUS_GLOBAL_MAPPINGS", mapping)
}

// 回归：可乐（colapt）官方组 COLAWEB 之前没进制作组映射表，归一成 team.other 后被
// exclude_teams 挡掉，声明为空。补齐映射后必须能正常生成「COLAWEB作品」声明。
func TestApplyTeamAcknowledgmentForColaWeb(t *testing.T) {
	setupAckTestConfig(t)

	sites := []SiteRow{
		{Group: "-COLAWEB", Description: "ColaPT"},
	}

	got, applied, reason := ApplyTeamAcknowledgmentIfNeeded("", "team.colaweb", "COLAWEB", sites)
	if !applied {
		t.Fatalf("期望插入声明，实际跳过 reason=%s", reason)
	}
	want := "[quote][b][color=blue][size=5]COLAWEB作品，感谢原制作者发布。[/size][/color][/b][/quote]"
	if got != want {
		t.Fatalf("声明内容异常:\n want %q\n got  %q", want, got)
	}
}

// 没匹配到标准组（team.other）时也要用解析出的原始制作组名补声明；
// 只有原始组名本身不可用时才跳过。
func TestApplyTeamAcknowledgmentFallsBackToRawGroup(t *testing.T) {
	setupAckTestConfig(t)

	sites := []SiteRow{{Group: "-COLAWEB", Description: "ColaPT"}}

	cases := []struct {
		name      string
		teamKey   string
		rawGroup  string
		wantApply bool
		wantName  string
	}{
		{name: "team.other + 原始组名", teamKey: "team.other", rawGroup: "PTTV", wantApply: true, wantName: "PTTV"},
		{name: "teamKey 为空 + 原始组名", teamKey: "", rawGroup: "StarfallWeb", wantApply: true, wantName: "StarfallWeb"},
		{name: "原始组名带前导横线", teamKey: "team.other", rawGroup: "-HDVWEB", wantApply: true, wantName: "HDVWEB"},
		{name: "原始组名为空", teamKey: "team.other", rawGroup: "", wantApply: false},
		{name: "占位符 NOGROUP 不生成", teamKey: "team.other", rawGroup: "NOGROUP", wantApply: false},
		{name: "含空白的误解析值不生成", teamKey: "team.other", rawGroup: "DDP 5.1", wantApply: false},
	}
	for _, tc := range cases {
		got, applied, reason := ApplyTeamAcknowledgmentIfNeeded("", tc.teamKey, tc.rawGroup, sites)
		if applied != tc.wantApply {
			t.Fatalf("%s: applied=%v reason=%s", tc.name, applied, reason)
		}
		if !tc.wantApply {
			if got != "" {
				t.Fatalf("%s: 跳过时不应修改声明，got=%q", tc.name, got)
			}
			continue
		}
		if !strings.Contains(got, tc.wantName+"作品，感谢原制作者发布。") {
			t.Fatalf("%s: 声明内容异常 %q", tc.name, got)
		}
	}
}

// 已有声明（源站写的或我们上次写的）不得重复追加。
func TestApplyTeamAcknowledgmentSkipsExistingStatement(t *testing.T) {
	setupAckTestConfig(t)

	sites := []SiteRow{{Group: "-COLAWEB", Description: "ColaPT"}}
	existing := "[quote][b][color=blue][size=5]COLAWEB作品，感谢原制作者发布。[/size][/color][/b][/quote]"
	if _, applied, reason := ApplyTeamAcknowledgmentIfNeeded(existing, "team.colaweb", "COLAWEB", sites); applied {
		t.Fatalf("已存在声明不应重复插入，reason=%s", reason)
	}

	source := "[quote]Audiences官组作品，感谢原制作者发布。[/quote]"
	if _, applied, reason := ApplyTeamAcknowledgmentIfNeeded(source, "team.other", "ADWeb", sites); applied {
		t.Fatalf("源站已带官组声明不应重复插入，reason=%s", reason)
	}

	// 制作组名较长时，整段 quote 会超过 max_statement_length(80)，pattern 兜不住，
	// 必须靠 detection_keywords 里的「感谢原制作者发布」识别，否则重复抓取会叠声明。
	longName := "StarfallWebRelease"
	longStatement := "[quote][b][color=blue][size=5]" + longName + "作品，感谢原制作者发布。[/size][/color][/b][/quote]"
	if utf8.RuneCountInString(longStatement) <= 80 {
		t.Fatalf("构造的样例未超过 80 字符，无法覆盖关键词兜底路径: %d", utf8.RuneCountInString(longStatement))
	}
	if _, applied, reason := ApplyTeamAcknowledgmentIfNeeded(longStatement, "team.other", longName, sites); applied {
		t.Fatalf("超长声明也应被关键词识别，reason=%s", reason)
	}
}
