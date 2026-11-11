package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"boxpilot/internal/gen/daemon"
	"github.com/egoist/mygo/ui"
)

func (a *App) tools(c *ui.Context) {
	a.pageTitle(c, "工具", "Tools", nil)
	ui.Row(c).Gap(10).Children(func() {
		ui.Button(c, a.t("网络质量", "Network Quality")).OnClick(func() { a.tool = "quality" })
		ui.Button(c, a.t("STUN 测试", "STUN Test")).OnClick(func() { a.tool = "stun" })
	})
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		ui.Column(c).FillWidth().Gap(16).Children(func() {
			a.card(c, a.t("测试配置", "Test configuration"), func() {
				if a.tool == "quality" {
					a.field(c, a.t("配置 URL", "Configuration URL"), func() {
						ui.TextInput(c.Key("quality-url"), &a.toolURL).FillWidth().Label(a.t("配置 URL", "Configuration URL"))
					})
					ui.Row(c).Gap(16).Wrap().Children(func() {
						runtimeValues := []string{"20", "30", "60"}
						runtimeOptions := []string{
							fmt.Sprintf(a.t("%s 秒", "%s seconds"), "20"),
							fmt.Sprintf(a.t("%s 秒", "%s seconds"), "30"),
							fmt.Sprintf(a.t("%s 秒", "%s seconds"), "60"),
						}
						runtimeOption := runtimeOptions[0]
						for i, value := range runtimeValues {
							if value == a.toolRuntime {
								runtimeOption = runtimeOptions[i]
							}
						}
						a.field(c, a.t("最大运行时间", "Max runtime"), func() {
							if ui.Select(c, &runtimeOption, runtimeOptions).Width(100).Changed() {
								for i, option := range runtimeOptions {
									if runtimeOption == option {
										a.toolRuntime = runtimeValues[i]
										break
									}
								}
							}
						})
						ui.Checkbox(c, &a.toolSerial, a.t("串行", "Serial"))
						ui.Checkbox(c, &a.toolHTTP3, "HTTP/3")
					})
				} else {
					a.field(c, a.t("服务器", "Server"), func() {
						ui.TextInput(c.Key("stun-server"), &a.toolServer).FillWidth().Label(a.t("服务器", "Server"))
					})
				}
				a.field(c, a.t("出站", "Outbound"), func() {
					outboundValues := []string{""}
					outboundOptions := []string{a.t("默认", "Default")}
					for _, item := range a.snapshot.Outbounds {
						outboundValues = append(outboundValues, item.GetTag())
						outboundOptions = append(outboundOptions, item.GetTag())
					}
					outboundOption := outboundOptions[0]
					for i, value := range outboundValues {
						if value == a.toolOutbound {
							outboundOption = outboundOptions[i]
						}
					}
					if ui.Select(c, &outboundOption, outboundOptions).FillWidth().Label(a.t("出站", "Outbound")).Changed() {
						for i, option := range outboundOptions {
							if outboundOption == option {
								a.toolOutbound = outboundValues[i]
								break
							}
						}
					}
				})
				ui.Row(c).Gap(10).Children(func() {
					ui.PrimaryButton(c, a.t("开始测试", "Start test")).Disabled(a.client == nil || a.busy["tool"]).OnClick(a.startTool)
					ui.Button(c, a.t("取消测试", "Cancel test")).Disabled(!a.busy["tool"]).OnClick(func() {
						if cancel := a.jobs["tool"]; cancel != nil {
							cancel()
						}
					})
					if a.busy["tool"] {
						ui.Spinner(c)
					}
				})
							})
			if a.toolError != "" {
				ui.Text(c, a.toolError).TextColor(a.palette.danger).Selectable()
			}
			if a.tool == "quality" && a.quality != nil {
				p := a.quality
				a.card(c, a.t("结果", "Results"), func() {
					a.line(c, a.t("空闲延迟", "Idle latency"), fmt.Sprintf("%d ms", p.GetIdleLatencyMs()))
					a.line(c, a.t("下载", "Download"), humanRate(p.GetDownloadCapacity()))
					a.line(c, a.t("上传", "Upload"), humanRate(p.GetUploadCapacity()))
					a.line(c, a.t("下载 RPM", "Download RPM"), fmt.Sprint(p.GetDownloadRPM()))
					a.line(c, a.t("上传 RPM", "Upload RPM"), fmt.Sprint(p.GetUploadRPM()))
					a.line(c, a.t("已用时间", "Elapsed"), fmt.Sprintf("%.1f s", float64(p.GetElapsedMs())/1000))
										if p.GetError() != "" {
						ui.Text(c, p.GetError()).TextColor(a.palette.danger)
					}
				})
			}
			if a.tool == "stun" && a.stun != nil {
				p := a.stun
				a.card(c, a.t("结果", "Results"), func() {
					a.line(c, a.t("外部地址", "External address"), p.GetExternalAddr())
					a.line(c, a.t("延迟", "Latency"), fmt.Sprintf("%d ms", p.GetLatencyMs()))
					a.line(c, a.t("NAT 映射", "NAT mapping"), natName(p.GetNatMapping()))
					a.line(c, a.t("NAT 过滤", "NAT filtering"), natFilter(p.GetNatFiltering()))
					if !p.GetNatTypeSupported() {
						a.line(c, a.t("NAT 类型检测", "NAT type detection"), a.t("服务器不支持", "Not supported by server"))
					}
										if p.GetError() != "" {
						ui.Text(c, p.GetError()).TextColor(a.palette.danger)
					}
				})
			}
		})
	})
}
func (a *App) startTool() {
	client := a.client
	if client == nil {
		return
	}
	generation := a.generation
	kind := a.tool
	requestURL, server, outbound := a.toolURL, a.toolServer, a.toolOutbound
	serial, http3 := a.toolSerial, a.toolHTTP3
	runtime, _ := strconv.Atoi(a.toolRuntime)
	a.quality = nil
	a.stun = nil
	a.toolError = ""
	a.do("tool", func(ctx context.Context) error {
		var err error
		if kind == "quality" {
			err = client.NetworkQuality(ctx, &daemon.NetworkQualityTestRequest{ConfigURL: requestURL, OutboundTag: outbound, Serial: serial, Http3: http3, MaxRuntimeSeconds: int32(runtime)}, func(p *daemon.NetworkQualityTestProgress) {
				a.post(func() {
					if a.generation == generation {
						a.quality = p
					}
				})
			})
		} else {
			err = client.STUN(ctx, &daemon.STUNTestRequest{Server: server, OutboundTag: outbound}, func(p *daemon.STUNTestProgress) {
				a.post(func() {
					if a.generation == generation {
						a.stun = p
					}
				})
			})
		}
		if err != nil && !errors.Is(err, context.Canceled) {
			a.post(func() {
				if a.generation == generation {
					a.toolError = err.Error()
				}
			})
		}
		return nil
	}, nil)
}
func natName(n int32) string {
	switch n {
	case 2:
		return "Endpoint Independent"
	case 3:
		return "Address Dependent"
	case 4:
		return "Address and Port Dependent"
	}
	return "Unknown"
}
func natFilter(n int32) string {
	switch n {
	case 1:
		return "Endpoint Independent"
	case 2:
		return "Address Dependent"
	case 3:
		return "Address and Port Dependent"
	}
	return "Unknown"
}
