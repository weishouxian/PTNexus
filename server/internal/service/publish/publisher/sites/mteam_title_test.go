package sites

import "testing"

// TestNormalizeMTeamTitle 覆盖馒头标题规范的两条纠偏：蓝光写法归一、帧率标记剔除。
// 帧率剔除的边界最容易出错，负向用例（不得误伤分辨率/位深/声道/HDR10+）与正向用例同样重要。
func TestNormalizeMTeamTitle(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		// 蓝光写法纠偏（原有用例，保持行为不变）
		{"Blu-ray 归一", "Movie.2024.1080p.Blu-ray.x264-GROUP", "Movie.2024.1080p.BluRay.x264-GROUP"},
		{"BLURAY 归一并剔除帧率", "Movie.2024.2160p.UHD.BLURAY.60fps.HDR-GROUP", "Movie.2024.2160p.UHD.BluRay.HDR-GROUP"},

		// 帧率剔除
		{"独立 token 结尾", "Some.Movie.2024.1080p.BluRay.x264.23.976FPS-GROUP", "Some.Movie.2024.1080p.BluRay.x264-GROUP"},
		{"独立 token 带空格", "Some.Movie.2024.1080p.BluRay.x264.60 fps-GROUP", "Some.Movie.2024.1080p.BluRay.x264-GROUP"},
		{"独立 token 独占尾部", "Some.Movie.1080p.119.88fps", "Some.Movie.1080p"},
		{"空格分隔标题", "Some Movie 2024 1080p BluRay x264 25fps GROUP", "Some Movie 2024 1080p BluRay x264 GROUP"},
		{"紧贴分辨率写法", "Some.Movie.2024.1080p.BluRay.2160p60fps.x264-GROUP", "Some.Movie.2024.1080p.BluRay.2160p.x264-GROUP"},
		{"紧贴分辨率且独占", "Movie.1080p60FPS", "Movie.1080p"},
		{"尾段点号被吃掉时不留孤立点", "Movie.2024.1080p.BluRay.x264-Group.60FPS", "Movie.2024.1080p.BluRay.x264-Group"},

		// 负向：不得误伤
		{"无帧率原样返回", "Movie.Name.2023.1080p.BluRay.DTS-HD.MA.5.1.x264-GROUP", "Movie.Name.2023.1080p.BluRay.DTS-HD.MA.5.1.x264-GROUP"},
		{"声道 7.1 不受影响", "Movie.2024.2160p.UHD.BluRay.REMUX.HDR.TrueHD.Atmos.7.1.x264-FraMeSToR", "Movie.2024.2160p.UHD.BluRay.REMUX.HDR.TrueHD.Atmos.7.1.x264-FraMeSToR"},
		{"HDR10+ 与 10bit 不受影响", "Movie.2024.1080p.BluRay.DTS-HD.MA.5.1.HDR10+.DV.HDR10+.HEVC-CtrlHD", "Movie.2024.1080p.BluRay.DTS-HD.MA.5.1.HDR10+.DV.HDR10+.HEVC-CtrlHD"},
		{"x264 编码号不被当成帧率", "Some.Movie.2024.1080p.BluRay.x264.60 fps-GROUP", "Some.Movie.2024.1080p.BluRay.x264-GROUP"},
		{"隔行 60.i 不受影响", "Movie.1080p.2160p.HDR10.DV.60.i.1080p", "Movie.1080p.2160p.HDR10.DV.60.i.1080p"},
		{"中日文标题原样保留", "完全沒有幀率的電影.2024.1080p.BluRay.x264-GROUP", "完全沒有幀率的電影.2024.1080p.BluRay.x264-GROUP"},

		// 兜底
		{"只有帧率时保留原标题", "60fps", "60fps"},
		{"空标题返回空", "", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeMTeamTitle(tc.in); got != tc.want {
				t.Fatalf("normalizeMTeamTitle 结果不符\nwant: %q\ngot:  %q", tc.want, got)
			}
		})
	}
}
