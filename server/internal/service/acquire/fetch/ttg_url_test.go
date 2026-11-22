package fetch

import "testing"

// TTG 详情页规范地址是 /t/{id}/，下载直链是 /dl/{id}/{passkey}。
// 详情页路径里没有 id= 参数，通用的 details.php 拼法在 TTG 上会拼错。
func TestTTGDetailAndDownloadURL(t *testing.T) {
	const baseURL = "https://totheglory.im"
	const passkey = "4e2cca4b983ba8039a0440a5eb6261ee"

	cases := []struct {
		name       string
		torrentID  string
		wantDetail string
		wantDL     string
	}{
		{
			name:       "裸数字 ID",
			torrentID:  "841600",
			wantDetail: "https://totheglory.im/t/841600/",
			wantDL:     "https://totheglory.im/dl/841600/" + passkey,
		},
		{
			name:       "详情页 URL",
			torrentID:  "https://totheglory.im/t/841600/",
			wantDetail: "https://totheglory.im/t/841600/",
			wantDL:     "https://totheglory.im/dl/841600/" + passkey,
		},
		{
			name:       "直接粘下载直链",
			torrentID:  "https://totheglory.im/dl/841600/" + passkey,
			wantDetail: "https://totheglory.im/t/841600/",
			wantDL:     "https://totheglory.im/dl/841600/" + passkey,
		},
		{
			name:       "相对下载路径",
			torrentID:  "/dl/841600/9945",
			wantDetail: "https://totheglory.im/t/841600/",
			wantDL:     "https://totheglory.im/dl/841600/9945",
		},
	}

	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			detail := buildDetailURL(baseURL, "ttg", item.torrentID)
			if detail != item.wantDetail {
				t.Fatalf("详情页构造异常：want %q, got %q", item.wantDetail, detail)
			}
			dl := buildDirectDownloadURL(baseURL, "ttg", detail, item.torrentID, passkey)
			if dl != item.wantDL {
				t.Fatalf("下载直链构造异常：want %q, got %q", item.wantDL, dl)
			}
		})
	}
}

// 站点只配了 Cookie、没有 passkey 时不能拼 download.php（TTG 没有这个入口）。
func TestTTGDownloadURLWithoutPasskey(t *testing.T) {
	const baseURL = "https://totheglory.im"
	detail := buildDetailURL(baseURL, "ttg", "841600")
	if detail != "https://totheglory.im/t/841600/" {
		t.Fatalf("详情页构造异常: %q", detail)
	}
	if dl := buildDirectDownloadURL(baseURL, "ttg", detail, "841600", ""); dl != "" {
		t.Fatalf("缺 passkey 时不应拼出直链，got %q", dl)
	}
}
