package gofins

// Command codes (Omron FINS specification, Cat. No. W342-E1-15)
const (
	CmdMemoryRead           uint16 = 0x0101 // IO memory area read
	CmdMemoryWrite          uint16 = 0x0102 // IO memory area write
	CmdMemoryFill           uint16 = 0x0103 // IO memory area fill
	CmdMemoryMultiRead      uint16 = 0x0104 // Multiple memory area read
	CmdMemoryTransfer       uint16 = 0x0105 // Memory area transfer
	CmdParamRead            uint16 = 0x0201 // Parameter area read
	CmdParamWrite           uint16 = 0x0202 // Parameter area write
	CmdParamClear           uint16 = 0x0203 // Parameter area clear
	CmdProgramRead          uint16 = 0x0301 // Program area read
	CmdProgramWrite         uint16 = 0x0302 // Program area write
	CmdProgramClear         uint16 = 0x0303 // Program area clear
	CmdRun                  uint16 = 0x0401 // Set operating mode to run
	CmdStop                 uint16 = 0x0402 // Set operating mode to stop
	CmdCPUDataRead          uint16 = 0x0501 // CPU unit data read
	CmdConnectionRead       uint16 = 0x0502 // Connection data read
	CmdStatusRead           uint16 = 0x0601 // CPU unit status read
	CmdCycleTimeRead        uint16 = 0x0620 // Cycle time read
	CmdClockRead            uint16 = 0x0701 // Clock read
	CmdClockWrite           uint16 = 0x0702 // Clock write
	CmdMessageReadClear     uint16 = 0x0920 // Message read/clear
	CmdAccessAcquire        uint16 = 0x0C01 // Access right acquire
	CmdAccessForcedAcquire  uint16 = 0x0C02 // Access right forced acquire
	CmdAccessRelease        uint16 = 0x0C03 // Access right release
	CmdErrorClear           uint16 = 0x2101 // Error clear
	CmdErrorLogRead         uint16 = 0x2102 // Error log read
	CmdErrorLogClear        uint16 = 0x2103 // Error log clear
	CmdFileRead             uint16 = 0x2201 // Single file read
	CmdFileWrite            uint16 = 0x2202 // Single file write
	CmdFileDelete           uint16 = 0x2205 // File delete
	CmdFileCopy             uint16 = 0x2207 // File copy
	CmdForcedSetReset       uint16 = 0x2301 // Forced set/reset
	CmdForcedSetResetCancel uint16 = 0x2302 // Forced set/reset cancel
)

// End codes (2-byte, per Omron spec)
const (
	EndCodeNormal               uint16 = 0x0000 // Normal completion
	EndCodeServiceInterrupted   uint16 = 0x0001 // Service interrupted
	EndCodeLocalNodeNotInNet    uint16 = 0x0101 // Local node not in network
	EndCodeTokenTimeout         uint16 = 0x0102 // Token timeout
	EndCodeRetriesFailed        uint16 = 0x0103 // Retries failed
	EndCodeTooManySendFrames    uint16 = 0x0104 // Too many send frames
	EndCodeNodeAddrRangeError   uint16 = 0x0105 // Node address range error
	EndCodeNodeAddrDuplication  uint16 = 0x0106 // Node address duplication
	EndCodeDestNotInNetwork     uint16 = 0x0201 // Destination not in network
	EndCodeUnitMissing          uint16 = 0x0202 // Unit missing
	EndCodeThirdNodeMissing     uint16 = 0x0203 // Third node missing
	EndCodeDestBusy             uint16 = 0x0204 // Destination busy
	EndCodeResponseTimeout      uint16 = 0x0205 // Response timeout
	EndCodeCommControllerErr    uint16 = 0x0301 // Communication controller error
	EndCodeCPUUnitError         uint16 = 0x0302 // CPU unit error
	EndCodeControllerError      uint16 = 0x0303 // Controller error
	EndCodeUnitNumberError      uint16 = 0x0304 // Unit number error
	EndCodeUndefinedCommand     uint16 = 0x0401 // Undefined command
	EndCodeNotSupported         uint16 = 0x0402 // Not supported by model/version
	EndCodeRoutingTableErr      uint16 = 0x0501 // Routing table error
	EndCodeNoRoutingTables      uint16 = 0x0502 // No routing tables
	EndCodeTooManyRelays        uint16 = 0x0504 // Too many relays
	EndCodeCommandTooLong       uint16 = 0x1001 // Command too long
	EndCodeCommandTooShort      uint16 = 0x1002 // Command too short
	EndCodeElementsDataMismatch uint16 = 0x1003 // Elements/data don't match
	EndCodeCommandFormatError   uint16 = 0x1004 // Command format error
	EndCodeHeaderError          uint16 = 0x1005 // Header error
	EndCodeAreaClassMissing     uint16 = 0x1101 // Area classification missing
	EndCodeAccessSizeError      uint16 = 0x1102 // Access size error
	EndCodeAddressRangeError    uint16 = 0x1103 // Address range error
	EndCodeAddressExceeded      uint16 = 0x1104 // Address range exceeded
	EndCodeProgramMissing       uint16 = 0x1106 // Program missing
	EndCodeRelationalError      uint16 = 0x1109 // Relational error
	EndCodeDuplicateDataAccess  uint16 = 0x110A // Duplicate data access
	EndCodeResponseTooBig       uint16 = 0x110B // Response too big
	EndCodeParameterError       uint16 = 0x110C // Parameter error
	EndCodeReadProtected        uint16 = 0x2002 // Read not possible: protected
	EndCodeWriteProtected       uint16 = 0x2102 // Write not possible: protected
	EndCodeWriteReadOnly        uint16 = 0x2101 // Write not possible: read only
	EndCodeNotExecDuringExec    uint16 = 0x2201 // Not executable during execution
	EndCodeNotExecWhileRunning  uint16 = 0x2202 // Not executable while running
	EndCodeNotExecInProgram     uint16 = 0x2203 // PLC in PROGRAM mode
	EndCodeNotExecInDebug       uint16 = 0x2204 // PLC in DEBUG mode
	EndCodeNotExecInMonitor     uint16 = 0x2205 // PLC in MONITOR mode
	EndCodeNotExecInRun         uint16 = 0x2206 // PLC in RUN mode
	EndCodeFileDeviceMissing    uint16 = 0x2301 // File device missing
	EndCodeMemoryMissing        uint16 = 0x2302 // Memory missing
	EndCodeClockMissing         uint16 = 0x2303 // Clock missing
	EndCodeUnitMemError         uint16 = 0x2502 // Memory error
	EndCodeUnitIOError          uint16 = 0x2503 // I/O error
	EndCodeCommandNoProtection  uint16 = 0x2601 // No protection
	EndCodeIncorrectPassword    uint16 = 0x2602 // Incorrect password
	EndCodeServiceExecuting     uint16 = 0x2605 // Service already executing
	EndCodeServiceStopped       uint16 = 0x2606 // Service stopped
	EndCodeNoAccessRight        uint16 = 0x3001 // No access right
	EndCodeServiceAborted       uint16 = 0x4001 // Service aborted
)

// Memory areas — each memory area has separate byte codes per Omron spec.
type MemoryArea byte

const (
	// Bit access
	MemAreaCIOBit MemoryArea = 0x30 // CIO area, bit access
	MemAreaWRBit  MemoryArea = 0x31 // Work area, bit access
	MemAreaHRBit  MemoryArea = 0x32 // Holding area, bit access
	MemAreaARBit  MemoryArea = 0x33 // Auxiliary area, bit access
	MemAreaDMBit  MemoryArea = 0x02 // Data memory, bit access

	// Word access
	MemAreaCIOWord MemoryArea = 0xB0 // CIO area, word access
	MemAreaWRWord  MemoryArea = 0xB1 // Work area, word access
	MemAreaHRWord  MemoryArea = 0xB2 // Holding area, word access
	MemAreaARWord  MemoryArea = 0xB3 // Auxiliary area, word access
	MemAreaDM      MemoryArea = 0x82 // Data memory, word access

	// Timer/counter
	MemAreaTimerCounterCompletionFlag MemoryArea = 0x09 // Timer/counter completion flag (bit)
	MemAreaTimerCounterPV             MemoryArea = 0x89 // Timer/counter present value (word)

	// Task flags
	MemAreaTaskBit    MemoryArea = 0x06 // Task flags, bit
	MemAreaTaskStatus MemoryArea = 0x46 // Task flags, status

	// Registers
	MemAreaIndexRegisterPV MemoryArea = 0xDC // Index register PV
	MemAreaDataRegisterPV  MemoryArea = 0xBC // Data register PV

	// Clock/Condition flags
	MemAreaClockPulsesConditionFlagsBit MemoryArea = 0x07
)

// ICF (Information Control Field) bit definitions.
// Bit 7: always 1 (bridge/network)
// Bit 6: 0=command, 1=response
// Bit 0: 0=response required, 1=no response
const (
	icfBridgeBit      byte = 0x80 // Always set
	icfMessageTypeBit byte = 0x40 // 0=command, 1=response
	icfNoResponseBit  byte = 0x01 // 1=no response expected

	ICFCommand           byte = 0x80 // Command, response required (bit7=1, bit6=0, bit0=0)
	ICFCommandNoResponse byte = 0x81 // Command, no response (bit7=1, bit6=0, bit0=1)
	ICFResponse          byte = 0xC0 // Response (bit7=1, bit6=1, bit0=0)
)

// Default FINS header values
const (
	DefaultRSV byte = 0x00 // Reserved (always 0)
	DefaultGCT byte = 0x02 // Gateway count (2 = direct connection)
)

// 说明：完整的 end code → 文本表在 endcodes.go（EndCodeToString 由那里提供，
// 覆盖 W342-E1-15 全部 85 个码，未知码也带上 16 进制）。

// MemoryAreaToString returns a human-readable name for the memory area.
func MemoryAreaToString(area MemoryArea) string {
	switch area {
	case MemAreaCIOBit:
		return "CIO (bit)"
	case MemAreaWRBit:
		return "WR (bit)"
	case MemAreaHRBit:
		return "HR (bit)"
	case MemAreaARBit:
		return "AR (bit)"
	case MemAreaDMBit:
		return "DM (bit)"
	case MemAreaCIOWord:
		return "CIO (word)"
	case MemAreaWRWord:
		return "WR (word)"
	case MemAreaHRWord:
		return "HR (word)"
	case MemAreaARWord:
		return "AR (word)"
	case MemAreaDM:
		return "DM (word)"
	case MemAreaTimerCounterCompletionFlag:
		return "Timer/Counter Flag"
	case MemAreaTimerCounterPV:
		return "Timer/Counter PV"
	case MemAreaTaskBit:
		return "Task Flag (bit)"
	case MemAreaTaskStatus:
		return "Task Flag (status)"
	case MemAreaIndexRegisterPV:
		return "Index Register"
	case MemAreaDataRegisterPV:
		return "Data Register"
	default:
		return "Unknown"
	}
}

// IsWordArea returns true if the memory area is word-accessible.
func IsWordArea(area MemoryArea) bool {
	switch area {
	case MemAreaDM, MemAreaCIOWord, MemAreaWRWord, MemAreaHRWord, MemAreaARWord,
		MemAreaTimerCounterPV, MemAreaDataRegisterPV, MemAreaIndexRegisterPV:
		return true
	default:
		return false
	}
}

// IsBitArea returns true if the memory area is bit-accessible.
func IsBitArea(area MemoryArea) bool {
	switch area {
	case MemAreaDMBit, MemAreaCIOBit, MemAreaWRBit, MemAreaHRBit, MemAreaARBit,
		MemAreaTimerCounterCompletionFlag, MemAreaTaskBit:
		return true
	default:
		return false
	}
}
