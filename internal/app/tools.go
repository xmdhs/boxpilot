package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/xmdhs/boxpilot/locales"
	"strconv"

	"github.com/egoist/mygo/ui"
	"github.com/xmdhs/boxpilot/internal/gen/daemon"
)

func (a *App) tools(c *ui.Context) {
	a.pageTitle(c, locales.In(a.language()).Tools(), nil)
	ui.Row(c).Gap(10).Children(func() {
		transitionedButton(c, locales.In(a.language()).NetworkQuality()).OnClick(func() { a.tool = "quality" })
		transitionedButton(c, locales.In(a.language()).StunTest()).OnClick(func() { a.tool = "stun" })
	})
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		ui.Column(c).FillWidth().Gap(16).Children(func() {
			a.card(c, locales.In(a.language()).TestConfiguration(), func() {
				if a.tool == "quality" {
					a.field(c, locales.In(a.language()).ConfigurationURL(), func() {
						ui.TextInput(c.Key("quality-url"), &a.toolURL).FillWidth().Label(locales.In(a.language()).ConfigurationURL())
					})
					ui.Row(c).Gap(16).Wrap().Children(func() {
						runtimeValues := []string{"20", "30", "60"}
						runtimeOptions := []string{
							locales.In(a.language()).DurationSeconds("20"),
							locales.In(a.language()).DurationSeconds("30"),
							locales.In(a.language()).DurationSeconds("60"),
						}
						runtimeOption := runtimeOptions[0]
						for i, value := range runtimeValues {
							if value == a.toolRuntime {
								runtimeOption = runtimeOptions[i]
							}
						}
						a.field(c, locales.In(a.language()).MaxRuntime(), func() {
							if transitionedSelect(c, &runtimeOption, runtimeOptions).Width(100).Changed() {
								for i, option := range runtimeOptions {
									if runtimeOption == option {
										a.toolRuntime = runtimeValues[i]
										break
									}
								}
							}
						})
						transitionedCheckbox(c, &a.toolSerial, locales.In(a.language()).Serial())
						transitionedCheckbox(c, &a.toolHTTP3, "HTTP/3")
					})
				} else {
					a.field(c, locales.In(a.language()).Server(), func() {
						ui.TextInput(c.Key("stun-server"), &a.toolServer).FillWidth().Label(locales.In(a.language()).Server())
					})
				}
				a.field(c, locales.In(a.language()).Outbound(), func() {
					outboundValues := []string{""}
					outboundOptions := []string{locales.In(a.language()).Default()}
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
					if transitionedSelect(c, &outboundOption, outboundOptions).FillWidth().Label(locales.In(a.language()).Outbound()).Changed() {
						for i, option := range outboundOptions {
							if outboundOption == option {
								a.toolOutbound = outboundValues[i]
								break
							}
						}
					}
				})
				ui.Row(c).Gap(10).Children(func() {
					transitionedPrimaryButton(c, locales.In(a.language()).StartTest()).Disabled(a.client == nil || a.busy["tool"]).OnClick(a.startTool)
					transitionedButton(c, locales.In(a.language()).CancelTest()).Disabled(!a.busy["tool"]).OnClick(func() {
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
				a.card(c, locales.In(a.language()).Results(), func() {
					a.line(c, locales.In(a.language()).IdleLatency(), fmt.Sprintf("%d ms", p.GetIdleLatencyMs()))
					a.line(c, locales.In(a.language()).Download(), humanRate(p.GetDownloadCapacity()))
					a.line(c, locales.In(a.language()).Upload(), humanRate(p.GetUploadCapacity()))
					a.line(c, locales.In(a.language()).DownloadRPM(), fmt.Sprint(p.GetDownloadRPM()))
					a.line(c, locales.In(a.language()).UploadRPM(), fmt.Sprint(p.GetUploadRPM()))
					a.line(c, locales.In(a.language()).Elapsed(), fmt.Sprintf("%.1f s", float64(p.GetElapsedMs())/1000))
					if p.GetError() != "" {
						ui.Text(c, p.GetError()).TextColor(a.palette.danger)
					}
				})
			}
			if a.tool == "stun" && a.stun != nil {
				p := a.stun
				a.card(c, locales.In(a.language()).Results(), func() {
					a.line(c, locales.In(a.language()).ExternalAddress(), p.GetExternalAddr())
					a.line(c, locales.In(a.language()).Latency(), fmt.Sprintf("%d ms", p.GetLatencyMs()))
					a.line(c, locales.In(a.language()).NatMapping(), natName(p.GetNatMapping()))
					a.line(c, locales.In(a.language()).NatFiltering(), natFilter(p.GetNatFiltering()))
					if !p.GetNatTypeSupported() {
						a.line(c, locales.In(a.language()).NatTypeDetection(), locales.In(a.language()).NotSupportedByServer())
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
