package downloaderclient

import (
	"reflect"
	"testing"
)

// TestMergeLabelListsKeepsExisting 验证合并标签时保留任务原有标签（源站标签不被目标站覆盖）。
func TestMergeLabelListsKeepsExisting(t *testing.T) {
	cases := []struct {
		name     string
		existing []string
		extra    []string
		want     []string
	}{
		{
			name:     "源站标签保留并追加目标站",
			existing: []string{"春天"},
			extra:    []string{"肉丝"},
			want:     []string{"春天", "肉丝"},
		},
		{
			name:     "重复标签只保留一份",
			existing: []string{"肉丝", "春天"},
			extra:    []string{"肉丝"},
			want:     []string{"肉丝", "春天"},
		},
		{
			name:     "空原有标签直接写入新标签",
			existing: nil,
			extra:    []string{"肉丝"},
			want:     []string{"肉丝"},
		},
		{
			name:     "带空白项会被清洗",
			existing: []string{" 春天 ", ""},
			extra:    []string{"", " 肉丝"},
			want:     []string{"春天", "肉丝"},
		},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			got := mergeLabelLists(item.existing, item.extra)
			if !reflect.DeepEqual(got, item.want) {
				t.Fatalf("mergeLabelLists(%v, %v) = %v, want %v", item.existing, item.extra, got, item.want)
			}
		})
	}
}

// TestTorrentLabelsParse 验证 torrent-get 返回的 labels（[]any / []string / string）都能解析。
func TestTorrentLabelsParse(t *testing.T) {
	if got := torrentLabels([]any{"春天", "", "肉丝"}); !reflect.DeepEqual(got, []string{"春天", "肉丝"}) {
		t.Fatalf("[]any 解析结果错误: %v", got)
	}
	if got := torrentLabels([]string{"春天", "春天"}); !reflect.DeepEqual(got, []string{"春天"}) {
		t.Fatalf("[]string 解析结果错误: %v", got)
	}
	if got := torrentLabels("春天"); !reflect.DeepEqual(got, []string{"春天"}) {
		t.Fatalf("string 解析结果错误: %v", got)
	}
	if got := torrentLabels(nil); len(got) != 0 {
		t.Fatalf("nil 应返回空列表, got %v", got)
	}
}
