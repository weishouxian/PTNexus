package repository

import (
	"reflect"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// newSiteOrderTestDB 构造一个只含 nickname/site/sort_order 的内存站点表。
func newSiteOrderTestDB(t *testing.T, rows [][3]any) *gorm.DB {
	t.Helper()
	dsn := "file:" + t.Name() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	if err := db.Exec("CREATE TABLE sites (nickname TEXT, site TEXT, sort_order INTEGER)").Error; err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	for _, row := range rows {
		if err := db.Exec("INSERT INTO sites (nickname, site, sort_order) VALUES (?, ?, ?)", row[0], row[1], row[2]).Error; err != nil {
			t.Fatalf("写入站点失败: %v", err)
		}
	}
	return db
}

// TestSortSiteNicknamesByPublishOrder 验证多站发布的目标站按站点管理的排序序号重排。
func TestSortSiteNicknamesByPublishOrder(t *testing.T) {
	db := newSiteOrderTestDB(t, [][3]any{
		{"馒头", "m-team", 1},
		{"猫站", "pterclub", 3},
		{"家园", "hdhome", 4},
		{"我堡", "ourbits", 5},
		{"同序B", "same-b", 6},
		{"同序A", "same-a", 6},
		{"未排序", "unranked", 0},
	})

	cases := []struct {
		name  string
		input []string
		want  []string
	}{
		{
			name:  "勾选顺序被站点排序覆盖",
			input: []string{"我堡", "馒头", "家园"},
			want:  []string{"馒头", "家园", "我堡"},
		},
		{
			name:  "sort_order=0 排末尾",
			input: []string{"未排序", "猫站", "馒头"},
			want:  []string{"馒头", "猫站", "未排序"},
		},
		{
			name:  "同序号按昵称",
			input: []string{"同序B", "同序A"},
			want:  []string{"同序A", "同序B"},
		},
		{
			name:  "站点代码同样参与排序",
			input: []string{"ourbits", "m-team"},
			want:  []string{"m-team", "ourbits"},
		},
		{
			name:  "库中不存在的站点保持原相对顺序排在最后",
			input: []string{"不存在B", "馒头", "不存在A"},
			want:  []string{"馒头", "不存在B", "不存在A"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SortSiteNicknamesByPublishOrder(db, tc.input)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("排序结果不符: got=%v want=%v", got, tc.want)
			}
			// 必须返回新切片，不能把调用方的 payload 顺序原地改掉。
			if len(tc.input) > 1 && reflect.DeepEqual(tc.input, got) && !reflect.DeepEqual(tc.want, tc.input) {
				t.Fatalf("入参被原地修改: %v", tc.input)
			}
		})
	}
}

// TestSortSiteNicknamesByPublishOrderNoop 验证空库/单元素/无 db 时原样返回，不阻断发布。
func TestSortSiteNicknamesByPublishOrderNoop(t *testing.T) {
	names := []string{"我堡", "馒头"}
	if got := SortSiteNicknamesByPublishOrder(nil, names); !reflect.DeepEqual(got, names) {
		t.Fatalf("无数据库时应原样返回: %v", got)
	}
	db := newSiteOrderTestDB(t, [][3]any{{"馒头", "m-team", 1}})
	if got := SortSiteNicknamesByPublishOrder(db, []string{"馒头"}); !reflect.DeepEqual(got, []string{"馒头"}) {
		t.Fatalf("单元素应原样返回: %v", got)
	}
}
