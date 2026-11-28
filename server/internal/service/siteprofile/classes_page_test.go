package siteprofile

import (
	"strings"
	"testing"
)

// classesPageFixture 是北洋园 tjupt `/classes.php` 的节选（三个等级行，从高到低排列），
// 结构照抄真实页面：第一列名称 span + 人数，第二列要求（`当前值/目标值`），第三列权限，
// 第四列状态。用来固定「classes.php 才是北洋园的等级判断页」这条解析口径。
const classesPageFixture = `
<table class="main" style='width: 97%'>
<tr>
<td>等级</td><td>要求/职责</td><td>新增权限</td><td>申请</td>
</tr>
    <tr id="6" class='active_class'>
    <td style="text-align: center; white-space: nowrap">
        <img src="pic/veteran.gif" alt="VeteranUser">
        <br>
        <span class='VeteranUser_Name' style='font-weight: bold'>威震一方</span>
        <br>
        <span class='VeteranUser_Name' style='font-weight: bold'>Veteran User</span>
        <br>
        目前人数：2810人
    </td>
    <td style="text-align: left">
        <ul>
            <li><span >注册周数：59/48</span></li>
            <li><span >完成种子数（体积&gt;200MiB）：800/600</span></li>
            <li><span >累计做种时间（天）：46723.75/4200</span></li>
            <li><span >上传量（GiB）：8585.97/5000</span></li>
            <li><span >发布种子数（现存且完成数≥1）：49/10</span></li>
            <li><span >H&amp;R积分：100/100</span></li>
            <li><span >升级所需花费魔力值：154823.7/300000</span></li>
        </ul>
    </td>
    <td style="text-align: left">
        <ul>
            <li><span style='color: gray'><s>首次升级至此等级时将获得1个永久邀请</s></span></li>
            <li>查看其它用户的评论、帖子历史</li>
            <li>永久保留账号</li>
        </ul>
    </td>
    <td style="text-align: center; white-space: nowrap">已达到此等级</td>
    </tr>
    <tr id="5" class='previous_class'>
    <td style="text-align: center; white-space: nowrap">
        <img src="pic/insane.gif" alt="InsaneUser">
        <br>
        <span class='InsaneUser_Name' style='font-weight: bold'>小有名气</span>
        <br>
        <span class='InsaneUser_Name' style='font-weight: bold'>Insane User</span>
        <br>
        目前人数：2884人
    </td>
    <td style="text-align: left">
        <ul>
            <li><span >注册周数：59/28</span></li>
            <li><span >上传量（GiB）：8585.97/2000</span></li>
        </ul>
    </td>
    <td style="text-align: left">
        <ul>
            <li>查看普通日志</li>
        </ul>
    </td>
    <td style="text-align: center; white-space: nowrap">已达到此等级</td>
    </tr>
    <tr id="1" class='previous_class'>
    <td style="text-align: center; white-space: nowrap">
        <img src="pic/user.gif" alt="User">
        <br>
        <span class='User_Name' style='font-weight: bold'>无名小辈</span>
        <br>
        <span class='User_Name' style='font-weight: bold'>User</span>
        <br>
        目前人数：18033人
    </td>
    <td style="text-align: left">
        初始等级，欢迎来到北洋园PT
    </td>
    <td style="text-align: left">
        <ul>
            <li>发布/下载种子</li>
        </ul>
    </td>
    <td style="text-align: center; white-space: nowrap">已达到此等级</td>
    </tr>
</table>`

// TestParseClassesPage 校验 classes.php 的等级行解析：名称拼接、由低到高排序、
// 要求按行保留、划线项剔除、保号只落在写了「永久保留账号」的那一级。
func TestParseClassesPage(t *testing.T) {
	levels := parseClassesPage(classesPageFixture)
	if len(levels) != 3 {
		t.Fatalf("等级数 = %d, want 3", len(levels))
	}
	// 表格是从高到低排的，解析后必须还原成由低到高。
	if levels[0].Name != "无名小辈(User)" || levels[2].Name != "威震一方(Veteran User)" {
		t.Fatalf("排序/名称不对: %q .. %q", levels[0].Name, levels[2].Name)
	}
	for index := range levels {
		if levels[index].Order != index {
			t.Fatalf("order 未按序号赋值: levels[%d].Order = %d", index, levels[index].Order)
		}
	}

	veteran := levels[2]
	for _, want := range []string{"注册周数：59/48", "上传量（GiB）：8585.97/5000", "H&R积分：100/100"} {
		if !strings.Contains(veteran.Requirements, want) {
			t.Fatalf("要求缺少 %q: %q", want, veteran.Requirements)
		}
	}
	if strings.Contains(veteran.Privileges, "永久邀请") {
		t.Fatalf("划线项（已获得）不该进权限列表: %q", veteran.Privileges)
	}
	if !strings.Contains(veteran.Privileges, "目前人数：2810人") {
		t.Fatalf("权限列表应带上目前人数: %q", veteran.Privileges)
	}

	if !veteran.KeepAlive {
		t.Fatalf("威震一方写了「永久保留账号」，应标为保号")
	}
	if levels[0].KeepAlive || levels[1].KeepAlive {
		t.Fatalf("低等级不该标保号")
	}
}

// TestParseClassesPageRejectsOtherPages 校验「不是 classes.php」的页面不会被误判成等级表。
func TestParseClassesPageRejectsOtherPages(t *testing.T) {
	for _, html := range []string{
		"",
		"<html><body>404 Not Found</body></html>",
		"<table><tr><td>a</td><td>b</td></tr></table>",
	} {
		if levels := parseClassesPage(html); len(levels) != 0 {
			t.Fatalf("不该解析出等级: %v", levels)
		}
	}
}
