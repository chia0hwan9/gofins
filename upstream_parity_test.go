package gofins

import (
	"errors"
	"testing"
)

// ---------- 上游对齐：状态/模式枚举、HasError、原始状态读、ICF 位 ----------

// 与 folke99/gofins 的 mapping.StatusCode / mapping.ModeCode + PLCStatus.HasError 对齐。
func TestPLCStatusHelpers(t *testing.T) {
	st := &PLCStatus{Status: byte(StatusRun), Mode: byte(ModeMonitor), FatalError: FatalIOBus | FatalProgram}

	if st.StatusCode().String() != "RUN" {
		t.Errorf("StatusCode().String() = %q", st.StatusCode().String())
	}
	if st.ModeCode().String() != "MONITOR" {
		t.Errorf("ModeCode().String() = %q", st.ModeCode().String())
	}
	if !st.IsRunning() || st.IsStopped() || st.IsStandby() {
		t.Errorf("运行状态判定有误: %+v", st)
	}
	if !st.IsMonitorMode() || st.IsProgramMode() || st.IsDebugMode() || st.IsRunMode() {
		t.Errorf("模式判定有误: %+v", st)
	}
	if !st.HasError(FatalIOBus) || !st.HasError(FatalProgram) {
		t.Error("HasError 应按标志位判断")
	}
	if st.HasError(FatalWatchDogTimer) {
		t.Error("未置位的标志不应报 true")
	}
	if !st.HasFatalError() {
		t.Error("有致命标志时应 HasFatalError")
	}

	// 未知取值也要给出可读文本（不是空串）
	if got := StatusCode(0x42).String(); got == "" || got == "RUN" {
		t.Errorf("未知状态码 → %q", got)
	}
	if got := ModeCode(0x42).String(); got == "" || got == "RUN" {
		t.Errorf("未知模式码 → %q", got)
	}
}

// ReadPLCStatus 返回未解析的 0601 响应（上游同名方法），字段布局由 Status() 负责。
func TestReadPLCStatusRaw(t *testing.T) {
	addr, _ := startSim(t)
	c := newSimClient(t, addr)

	resp, err := c.ReadPLCStatus()
	if err != nil {
		t.Fatalf("ReadPLCStatus: %v", err)
	}
	if resp.Command != CmdStatusRead {
		t.Errorf("Command = 0x%04X，期望 0x%04X", resp.Command, CmdStatusRead)
	}
	if resp.EndCode != EndCodeNormal {
		t.Errorf("EndCode = 0x%04X", resp.EndCode)
	}
	if len(resp.Data) < 18 {
		t.Errorf("状态响应数据 %d 字节，期望 >= 18", len(resp.Data))
	}
}

func TestHeaderIsResponseRequired(t *testing.T) {
	if !NewCommandHeader(1, 0, 2, 0, 1).IsResponseRequired() {
		t.Error("ICF=0x80 表示需要响应")
	}
	noResp := Header{ICF: ICFCommandNoResponse}
	if noResp.IsResponseRequired() {
		t.Error("ICF=0x81 表示不需要响应")
	}
	resp := Header{ICF: ICFResponse}
	if !resp.IsResponseRequired() {
		t.Error("ICF=0xC0（响应）不置 no-response 位")
	}
}

// ---------- 上游对齐：模拟器地址越界回 0x1104 ----------

// l1va 的模拟器会做地址越界检查；我们的模拟器之前不做，越界读会静默返回 0。
func TestSimulatorAddressBounds(t *testing.T) {
	addr, _ := startSim(t)
	c := newSimClient(t, addr)

	// 字区越界：D32767 起读 2 个字 → 0x1104
	_, err := c.ReadWords(MemAreaDM, 32767, 2)
	var ecErr EndCodeError
	if !errors.As(err, &ecErr) || ecErr.Code != EndCodeAddressExceeded {
		t.Fatalf("越界字读应回 0x%04X，实际 %T: %v", EndCodeAddressExceeded, err, err)
	}

	// 边界内最后两个字可以读
	if _, err := c.ReadWords(MemAreaDM, 32766, 2); err != nil {
		t.Errorf("边界内读失败: %v", err)
	}

	// 位区越界：CIO 32767 起读 32 位（跨到 32768/32769）→ 0x1104
	_, err = c.ReadBits(MemAreaCIOBit, 32767, 0, 32)
	if !errors.As(err, &ecErr) || ecErr.Code != EndCodeAddressExceeded {
		t.Fatalf("越界位读应回 0x%04X，实际 %T: %v", EndCodeAddressExceeded, err, err)
	}

	// 越界写同理
	err = c.WriteWords(MemAreaDM, 32767, []uint16{1, 2})
	if !errors.As(err, &ecErr) || ecErr.Code != EndCodeAddressExceeded {
		t.Fatalf("越界写应回 0x%04X，实际 %T: %v", EndCodeAddressExceeded, err, err)
	}
}
