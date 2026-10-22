// SPDX-License-Identifier: MIT

package ieee20305

import "time"

// --------------------------------------------------------------------
// Primitive types
// --------------------------------------------------------------------

// HexBinary160 is a 160-bit (20-byte) value represented as 40 hex
// characters. It is the type of the Long-Form Device Identifier (LFDI),
// derived from the SHA-256 fingerprint of a device certificate.
type HexBinary160 [20]byte

// HexBinary128 is a 128-bit (16-byte) value represented as 32 hex
// characters. It is used for various identifiers in the standard.
type HexBinary128 [16]byte

// HexBinary64 is a 64-bit (8-byte) value represented as 16 hex
// characters.
type HexBinary64 [8]byte

// HexBinary48 is a 48-bit (6-byte) value represented as 12 hex
// characters.
type HexBinary48 [6]byte

// HexBinary32 is a 32-bit (4-byte) value represented as 8 hex
// characters.
type HexBinary32 [4]byte

// HexBinary16 is a 16-bit (2-byte) value represented as 4 hex
// characters.
type HexBinary16 [2]byte

// HexBinary8 is an 8-bit (1-byte) value represented as 2 hex
// characters.
type HexBinary8 [1]byte

// SFDIType is the Short-Form Device Identifier, a 5-byte (40-bit)
// value derived from the certificate fingerprint. It is typically
// represented as a left-truncation of the certificate fingerprint.
type SFDIType uint64

// MRIDType is a Meter Reading ID, an unsigned 128-bit integer used as
// a unique identifier for metering resources.
type MRIDType HexBinary128

// PENType is a Private Enterprise Number assigned by IANA.
type PENType uint32

// PowerOfTenMultiplier is an exponent of ten, ranging from -9 to +9,
// used to scale metering values.
type PowerOfTenMultiplier int8

// TOUType is a Time-of-Use tier identifier.
type TOUType uint8

// RoleFlagsType is a bitmask of the roles a device plays (mirror,
// premises aggregation point, etc.).
type RoleFlagsType uint16

// Role flags for RoleFlagsType.
const (
	RoleMirror                RoleFlagsType = 1 << 0
	RolePremisesAggregation   RoleFlagsType = 1 << 1
	RolePEV                   RoleFlagsType = 1 << 2
	RoleDER                   RoleFlagsType = 1 << 3
	RoleEVSE                  RoleFlagsType = 1 << 4
	RoleReserver              RoleFlagsType = 1 << 5
	RoleSmartInverter         RoleFlagsType = 1 << 6
	RoleESI                   RoleFlagsType = 1 << 7
	RoleSmartCharger          RoleFlagsType = 1 << 8
	RoleStorageDevice         RoleFlagsType = 1 << 9
)

// DeviceCategoryType identifies the kind of device by a bitmask.
type DeviceCategoryType uint32

// Device categories defined by IEEE 2030.5.
const (
	DeviceCategoryProgrammableCommunicatingThermostat DeviceCategoryType = 1 << 0
	DeviceCategoryStripHeater                         DeviceCategoryType = 1 << 1
	DeviceCategoryWaterHeater                         DeviceCategoryType = 1 << 2
	DeviceCategoryPoolPump                            DeviceCategoryType = 1 << 3
	DeviceCategorySauna                               DeviceCategoryType = 1 << 4
	DeviceCategoryHotTub                              DeviceCategoryType = 1 << 5
	DeviceCategorySmartAppliance                      DeviceCategoryType = 1 << 6
	DeviceCategoryIrrigationPump                      DeviceCategoryType = 1 << 7
	DeviceCategoryManagedCommercialIndustrial          DeviceCategoryType = 1 << 8
	DeviceCategorySimpleMiscLoad                      DeviceCategoryType = 1 << 9
	DeviceCategoryExteriorLighting                    DeviceCategoryType = 1 << 10
	DeviceCategoryInteriorLighting                    DeviceCategoryType = 1 << 11
	DeviceCategoryEV                                  DeviceCategoryType = 1 << 12
	DeviceCategoryEVSE                                DeviceCategoryType = 1 << 13
	DeviceCategoryVirtualMixed                        DeviceCategoryType = 1 << 14
	DeviceCategoryReciprocatingEngine                 DeviceCategoryType = 1 << 15
	DeviceCategoryFuelCell                            DeviceCategoryType = 1 << 16
	DeviceCategoryPhotovoltaic                        DeviceCategoryType = 1 << 17
	DeviceCategoryCombinedHeatPower                   DeviceCategoryType = 1 << 18
	DeviceCategoryOtherGeneration                     DeviceCategoryType = 1 << 19
	DeviceCategoryBattery                             DeviceCategoryType = 1 << 20
)

// ConnectStatusType indicates the connection status of a DER.
type ConnectStatusType uint8

// Connection status values.
const (
	ConnectStatusConnected    ConnectStatusType = 0
	ConnectStatusAvailable    ConnectStatusType = 1
	ConnectStatusOperating    ConnectStatusType = 2
	ConnectStatusTest         ConnectStatusType = 3
	ConnectStatusNotAvailable ConnectStatusType = 4
)

// InverterStatusType indicates the operating mode of an inverter.
type InverterStatusType uint8

// Inverter status values.
const (
	InverterStatusNA       InverterStatusType = 0
	InverterStatusOff      InverterStatusType = 1
	InverterStatusSleeping InverterStatusType = 2
	InverterStatusStarting InverterStatusType = 3
	InverterStatusTracking InverterStatusType = 4
	InverterStatusForced   InverterStatusType = 5
	InverterStatusShutting InverterStatusType = 6
	InverterStatusFault    InverterStatusType = 7
	InverterStatusStandby  InverterStatusType = 8
)

// DERType identifies the type of distributed energy resource.
type DERType uint8

// DER type values.
const (
	DERTypeNA                     DERType = 0
	DERTypeVirtualMixed           DERType = 1
	DERTypeReciprocatingEngine    DERType = 2
	DERTypeFuelCell               DERType = 3
	DERTypePhotovoltaic           DERType = 4
	DERTypeCombinedHeatPower      DERType = 5
	DERTypeOtherGeneration        DERType = 6
	DERTypeBatteryStorage         DERType = 80
	DERTypeEV                     DERType = 81
	DERTypeEVSE                   DERType = 82
	DERTypeCombinedPVStorage      DERType = 83
)

// DOESupportedMode is a bitmask indicating which DER Operating Envelope
// (DOE) modes a DER supports, as defined for CSIP-AUS.
type DOESupportedMode uint32

// DOE mode bits for CSIP-AUS.
const (
	DOEModeOpModExpLimW      DOESupportedMode = 1 << 0
	DOEModeOpModImpLimW      DOESupportedMode = 1 << 1
	DOEModeOpModGenLimW      DOESupportedMode = 1 << 2
	DOEModeOpModLoadLimW     DOESupportedMode = 1 << 3
	DOEModeOpModExpLimVar    DOESupportedMode = 1 << 4
	DOEModeOpModImpLimVar    DOESupportedMode = 1 << 5
)

// --------------------------------------------------------------------
// Base resource types
// --------------------------------------------------------------------

// Resource is the base type of all IEEE 2030.5 resources. Every
// addressable object carries an Href that identifies it.
type Resource struct {
	// Href is the URI of this resource relative to the server root.
	Href string
}

// Link references a related resource by its Href.
type Link struct {
	Href string
}

// ListLink references a list resource and includes the count of items.
type ListLink struct {
	Href string
	All  uint32
}

// SubscribableResource extends Resource with subscription support.
type SubscribableResource struct {
	Resource
	// Subscribable indicates whether this resource supports
	// subscriptions. 0 = not subscribable, 1 = subscribable.
	Subscribable uint8
}

// IdentifiedObject extends Resource with an MRID and a human-readable
// description.
type IdentifiedObject struct {
	Resource
	// MRID is a globally unique identifier for this object.
	MRID MRIDType
	// Description is a human-readable description of this object.
	Description string
	// Version is the version of this object; incremented on each
	// modification.
	Version uint16
}

// RespondableResource extends Resource with the indication of whether
// a response is required.
type RespondableResource struct {
	Resource
	// ReplyTo is the URI to which a response should be sent.
	ReplyTo string
	// ResponseRequired indicates which responses are required.
	// See ResponseRequired constants.
	ResponseRequired ResponseRequired
}

// RespondableSubscribableIdentifiedObject combines the base types of
// resources that are identified, subscribable and require a response,
// such as events and controls.
type RespondableSubscribableIdentifiedObject struct {
	IdentifiedObject
	// ReplyTo is the URI to which a response should be sent.
	ReplyTo string
	// ResponseRequired indicates which responses are required.
	ResponseRequired ResponseRequired
	// Subscribable indicates whether this resource supports
	// subscriptions.
	Subscribable uint8
}

// --------------------------------------------------------------------
// Device types
// --------------------------------------------------------------------

// EndDevice represents a device in the IEEE 2030.5 network. It is
// identified by its LFDI (Long-Form Device Identifier) derived from its
// certificate, and its SFDI (Short-Form Device Identifier).
type EndDevice struct {
	SubscribableResource
	// DeviceCategory is a bitmask of the kind of end device.
	DeviceCategory DeviceCategoryType
	// LFDI is the long-form device identifier: a 160-bit fingerprint
	// of the device certificate.
	LFDI HexBinary160
	// SFDI is the short-form device identifier, a left-truncated form
	// of the certificate fingerprint.
	SFDI SFDIType
	// ChangedTime is the time this EndDevice was last modified.
	ChangedTime time.Time
	// Enabled indicates whether this device is currently enabled.
	Enabled bool
	// PostRate is the default rate at which this device should post
	// readings, in seconds.
	PostRate uint32

	// Links to related resources.
	ConfigurationLink           *Link
	DERListLink                 *ListLink
	DeviceInformationLink       *Link
	DeviceStatusLink            *Link
	FileStatusLink              *Link
	FlowReservationRequestListLink *ListLink
	FlowReservationResponseListLink *ListLink
	FunctionSetAssignmentsListLink *ListLink
	LogEventListLink            *ListLink
	PowerStatusLink             *Link
	RegistrationLink            *Link
	SubscriptionListLink        *ListLink
}

// SelfDevice is the requesting device's own EndDevice resource.
type SelfDevice struct {
	EndDevice
}

// Registration holds the registration information of an end device on
// a server. The server creates it when the device first connects.
type Registration struct {
	Resource
	// DateTimeRegistered is the time the device was registered.
	DateTimeRegistered time.Time
	// PIN is the numeric Personal Identification Number used for
	// out-of-band device registration.
	PIN uint32
	// PollRate is the rate in seconds at which the device should poll.
	PollRate uint32
}

// DeviceCapability is the root resource that lists links to all
// function sets supported by the server.
type DeviceCapability struct {
	Resource
	// PollRate is the rate in seconds at which the device should poll
	// the server.
	PollRate uint32

	// Links to top-level function sets.
	EndDeviceListLink             *ListLink
	MirrorUsagePointListLink      *ListLink
	SelfDeviceLink                *Link
	TimeLink                      *Link
	UsagePointListLink            *ListLink
	DERProgramListLink            *ListLink
	MessagingProgramListLink      *ListLink
	ResponseSetListLink           *ListLink
}

// --------------------------------------------------------------------
// Function sets and DER programs
// --------------------------------------------------------------------

// FunctionSetAssignments binds a set of function sets (DER programs,
// demand-response programs, metering, etc.) to an end device or group.
type FunctionSetAssignments struct {
	Resource
	// CustomerAccountListLink links to the customer accounts.
	CustomerAccountListLink *ListLink
	// DemandResponseProgramListLink links to the demand response programs.
	DemandResponseProgramListLink *ListLink
	// DERProgramListLink links to the DER programs assigned.
	DERProgramListLink *ListLink
	// MessagingProgramListLink links to the messaging programs.
	MessagingProgramListLink *ListLink
	// PrepaymentListLink links to the prepayment resources.
	PrepaymentListLink *ListLink
	// ResponseSetListLink links to the response sets.
	ResponseSetListLink *ListLink
	// TariffProfileListLink links to the tariff profiles.
	TariffProfileListLink *ListLink
	// TimeLink links to the server time resource.
	TimeLink *Link
	// UsagePointListLink links to the usage points.
	UsagePointListLink *ListLink
}

// DERProgram describes a program of DER controls issued by the utility.
// It contains links to the controls, the default control, and the active
// control schedule.
type DERProgram struct {
	IdentifiedObject
	// Primacy is the priority of this program. Lower values indicate
	// higher priority. See Primacy.
	Primacy Primacy

	// Links to related resources.
	ActiveDERControlListLink  *ListLink
	DefaultDERControlLink     *Link
	DERControlListLink        *ListLink
	DERCurveListLink          *ListLink
}

// Primacy is the priority level of a DER program. Lower values indicate
// higher priority. The standard defines ranges:
//
//	0       = utility direct control (highest)
//	1-9     = utility programs
//	10-39   = aggregator/retailer programs
//	40-69   = facility/premises programs
//	70-255  = device default / autonomous
type Primacy uint8

// Standard primacy levels.
const (
	PrimacyUtilityDirect Primacy = 0
	PrimacyUtilityLow    Primacy = 9
	PrimacyAggregator    Primacy = 10
	PrimacyFacility      Primacy = 40
	PrimacyDeviceDefault Primacy = 70
)

// --------------------------------------------------------------------
// DER controls
// --------------------------------------------------------------------

// DERControlBase holds the operational parameters that a DER control
// can set. Each field is optional; a nil pointer means the control does
// not constrain that parameter.
type DERControlBase struct {
	// OpModConnect controls whether the DER connects to the grid.
	// true = connect, false = disconnect.
	OpModConnect *bool
	// OpModEnergize controls whether the DER energizes.
	// true = energize, false = de-energize.
	OpModEnergize *bool
	// OpModExpLimW is the maximum export (injection) power limit in
	// watts. The device must not export more than this value.
	OpModExpLimW *ActivePower
	// OpModImpLimW is the maximum import (consumption) power limit in
	// watts. The device must not import more than this value.
	OpModImpLimW *ActivePower
	// OpModGenLimW is the maximum generation power limit in watts.
	OpModGenLimW *ActivePower
	// OpModLoadLimW is the maximum load power limit in watts.
	OpModLoadLimW *ActivePower
	// OpModFixedPFAbsorbW is the fixed power factor in absorbing mode.
	OpModFixedPFAbsorbW *FixedPowerFactor
	// OpModFixedPFInjectW is the fixed power factor in injecting mode.
	OpModFixedPFInjectW *FixedPowerFactor
	// OpModFixedVar is a fixed reactive power setting.
	OpModFixedVar *ReactivePower
	// OpModFixedW is a fixed active power setting.
	OpModFixedW *ActivePower
	// OpModMaxLimW is the maximum active power the device may produce.
	OpModMaxLimW *ActivePower
	// OpModTargetW is the target active power output.
	OpModTargetW *ActivePower
	// OpModTargetVar is the target reactive power output.
	OpModTargetVar *ReactivePower
	// OpModVoltVar references a volt-var curve.
	OpModVoltVar *DERCurveLink
	// OpModWattVar references a watt-var curve.
	OpModWattVar *DERCurveLink
	// OpModFreqWatt references a frequency-watt curve.
	OpModFreqWatt *DERCurveLink
	// OpModHFRTMustTrip references a high-frequency ride-through curve.
	OpModHFRTMustTrip *DERCurveLink
	// OpModHFRTMayTrip references a high-frequency ride-through
	// may-trip curve.
	OpModHFRTMayTrip *DERCurveLink
	// OpModLFRTMustTrip references a low-frequency ride-through curve.
	OpModLFRTMustTrip *DERCurveLink
	// OpModHVRTMustTrip references a high-voltage ride-through curve.
	OpModHVRTMustTrip *DERCurveLink
	// OpModHVRTMomentaryCessation references a high-voltage momentary
	// cessation curve.
	OpModHVRTMomentaryCessation *DERCurveLink
	// OpModHVRTMayTrip references a high-voltage ride-through
	// may-trip curve.
	OpModHVRTMayTrip *DERCurveLink
	// OpModLVRTMustTrip references a low-voltage ride-through curve.
	OpModLVRTMustTrip *DERCurveLink
	// OpModLVRTMomentaryCessation references a low-voltage momentary
	// cessation curve.
	OpModLVRTMomentaryCessation *DERCurveLink
	// OpModLVRTMayTrip references a low-voltage ride-through
	// may-trip curve.
	OpModLVRTMayTrip *DERCurveLink
	// OpModVoltWatt references a volt-watt curve.
	OpModVoltWatt *DERCurveLink

	// CSIP-AUS extensions.

	// OpModExpLimVar is the maximum export reactive power limit in
	// var (CSIP-AUS extension).
	OpModExpLimVar *ReactivePower
	// OpModImpLimVar is the maximum import reactive power limit in
	// var (CSIP-AUS extension).
	OpModImpLimVar *ReactivePower
}

// DERCurveLink is a reference to a DERCurve resource by Href.
type DERCurveLink struct {
	Href string
}

// ActivePower represents an active power value in watts.
type ActivePower struct {
	// Multiplier is the power of ten to apply to Value.
	Multiplier PowerOfTenMultiplier
	// Value is the power in watts, before the multiplier is applied.
	Value int16
}

// ReactivePower represents a reactive power value in var.
type ReactivePower struct {
	// Multiplier is the power of ten to apply to Value.
	Multiplier PowerOfTenMultiplier
	// Value is the reactive power in var, before the multiplier.
	Value int16
}

// FixedPowerFactor represents a fixed power factor setting.
type FixedPowerFactor struct {
	// Displacement is the power factor displacement, 0-1000
	// (representing 0.000 to 1.000).
	Displacement uint16
	// Excitation indicates the excitation mode: true = absorbing
	// (lagging), false = injecting (leading).
	Excitation bool
}

// DERControl is a specific control event issued under a DER program.
// It has a scheduled time range and a set of DER operating parameters.
type DERControl struct {
	RespondableSubscribableIdentifiedObject
	DERControlBase DERControlBase
	// EventStatus describes the current state of this control event.
	EventStatus EventStatus
	// CreationTime is when this control was created by the server.
	CreationTime time.Time
	// StartTime is the scheduled start time of this control.
	StartTime time.Time
	// Duration is how long this control is active.
	Duration time.Duration
	// RandomizeDuration is the maximum randomization applied to
	// Duration, as a signed value. The device adds a random offset
	// in [-RandomizeDuration/2, +RandomizeDuration/2].
	RandomizeDuration time.Duration
	// RandomizeStart is the maximum randomization applied to
	// StartTime. The device adds a random offset in
	// [0, RandomizeStart].
	RandomizeStart time.Duration
}

// DefaultDERControl defines the default DER parameters that apply when
// no scheduled DER control event is active.
type DefaultDERControl struct {
	IdentifiedObject
	DERControlBase DERControlBase
}

// --------------------------------------------------------------------
// Event types
// --------------------------------------------------------------------

// EventStatusValue represents the status of a scheduled event.
type EventStatusValue uint8

// Event status values defined by the standard.
const (
	// EventStatusScheduled means the event is scheduled but not yet
	// active.
	EventStatusScheduled EventStatusValue = 0
	// EventStatusActive means the event is currently active.
	EventStatusActive EventStatusValue = 1
	// EventStatusCancelled means the event was cancelled by the
	// server before becoming active, or while active.
	EventStatusCancelled EventStatusValue = 2
	// EventStatusCancelledRandom means the event was cancelled with
	// randomized end.
	EventStatusCancelledRandom EventStatusValue = 3
	// EventStatusSuperseded means the event was superseded by a
	// higher-priority event.
	EventStatusSuperseded EventStatusValue = 4
)

// EventStatus describes the current status of an event and when it
// changed.
type EventStatus struct {
	// CurrentStatus is the current status of the event.
	CurrentStatus EventStatusValue
	// DateTime is the time this status was set.
	DateTime time.Time
	// PotentiallySuperseded indicates the event may have been
	// superseded by a higher-primacy control.
	PotentiallySuperseded bool
	// PotentiallySupersededTime is when the potential supersession
	// was detected.
	PotentiallySupersededTime time.Time
}

// --------------------------------------------------------------------
// DER state and capability types
// --------------------------------------------------------------------

// DER represents a single distributed energy resource associated with
// an end device. An end device may have multiple DER instances.
type DER struct {
	SubscribableResource
	// DERAvailabilityLink links to the current availability.
	DERAvailabilityLink *Link
	// DERCapabilityLink links to the nameplate capability.
	DERCapabilityLink *Link
	// DERSettingsLink links to the current operating settings.
	DERSettingsLink *Link
	// DERStatusLink links to the current operating status.
	DERStatusLink *Link
}

// DERCapability describes the nameplate ratings and capabilities of a
// DER. These do not change during operation.
type DERCapability struct {
	Resource
	// Type is the kind of DER (PV, battery, etc.).
	Type DERType
	// RtgMaxW is the nameplate maximum active power output in watts.
	RtgMaxW ActivePower
	// RtgMaxVar is the nameplate maximum reactive power in var.
	RtgMaxVar *ReactivePower
	// RtgMaxVA is the nameplate maximum apparent power in VA.
	RtgMaxVA *ApparentPower
	// RtgMaxChargeRateW is the maximum charge rate in watts (storage).
	RtgMaxChargeRateW *ActivePower
	// RtgMaxDischargeRateW is the maximum discharge rate in watts.
	RtgMaxDischargeRateW *ActivePower
	// RtgMaxEnergyStorageCapacity is the maximum stored energy in Wh.
	RtgMaxEnergyStorageCapacity *ActivePower
	// RtgMinPFOverExcited is the minimum power factor, overexcited.
	RtgMinPFOverExcited *PowerFactor
	// RtgMinPFUnderExcited is the minimum power factor, underexcited.
	RtgMinPFUnderExcited *PowerFactor
	// DOESupportedModes is the set of operating envelope modes
	// supported (CSIP-AUS).
	DOESupportedModes DOESupportedMode
}

// ApparentPower represents an apparent power value in VA.
type ApparentPower struct {
	Multiplier PowerOfTenMultiplier
	Value      uint16
}

// PowerFactor represents a power factor value (0-1000, i.e. 0.000 to
// 1.000) with excitation mode.
type PowerFactor struct {
	Displacement uint16
	Excitation   bool
}

// DERSettings describes the current configurable settings of a DER as
// reported by the device.
type DERSettings struct {
	SubscribableResource
	// UpdatedTime is when these settings were last reported.
	UpdatedTime time.Time
	// SetMaxW is the configured maximum active power output in watts.
	SetMaxW *ActivePower
	// SetMaxVar is the configured maximum reactive power in var.
	SetMaxVar *ReactivePower
	// SetMaxChargeRateW is the configured maximum charge rate.
	SetMaxChargeRateW *ActivePower
	// SetMaxDischargeRateW is the configured maximum discharge rate.
	SetMaxDischargeRateW *ActivePower
	// SetStorConnect indicates the default mode for storage upon
	// connection.
	SetStorConnect *bool
	// DOEModesSupportedByDevice lists the DOE modes the device
	// currently supports (CSIP-AUS).
	DOEModesSupportedByDevice DOESupportedMode
}

// DERStatus describes the current operating state of a DER as reported
// by the device.
type DERStatus struct {
	SubscribableResource
	// ReadingTime is when this status was sampled.
	ReadingTime time.Time
	// GenConnectStatus is the generation connection status.
	GenConnectStatus *ConnectStatusLink
	// StorConnectStatus is the storage connection status.
	StorConnectStatus *ConnectStatusLink
	// InverterStatus is the current inverter operating mode.
	InverterStatus *InverterStatusLink
	// StateOfChargeStatus is the battery state of charge as a
	// percentage (0-10000, representing 0.00%-100.00%).
	StateOfChargeStatus *PercentType
}

// ConnectStatusLink wraps a ConnectStatusType with its reading time.
type ConnectStatusLink struct {
	DateTime time.Time
	Value    ConnectStatusType
}

// InverterStatusLink wraps an InverterStatusType with its reading time.
type InverterStatusLink struct {
	DateTime time.Time
	Value    InverterStatusType
}

// PercentType is a percentage value scaled to 0-10000, representing
// 0.00% to 100.00%.
type PercentType uint16

// DERAvailability describes when and how much a DER is available for
// dispatch.
type DERAvailability struct {
	SubscribableResource
	// ReadingTime is when this availability was sampled.
	ReadingTime time.Time
	// AvailabilityDuration is how long the device is available from
	// ReadingTime, in seconds.
	AvailabilityDuration *uint32
	// MaxChargeDuration is the maximum duration the device can charge
	// at maximum rate, in seconds.
	MaxChargeDuration *uint32
	// EstimatedChargeRemaining is the estimated remaining energy as
	// a percentage (0-10000).
	EstimatedChargeRemaining *PercentType
	// EstimatedVarAvail is the estimated available reactive power.
	EstimatedVarAvail *ReactivePower
	// EstimatedWAvail is the estimated available active power.
	EstimatedWAvail *ActivePower
}

// --------------------------------------------------------------------
// DER curves
// --------------------------------------------------------------------

// CurveType identifies the kind of DER curve.
type CurveType uint8

// Curve types defined by the standard.
const (
	CurveTypeOpModVoltVar           CurveType = 0
	CurveTypeOpModFreqWatt          CurveType = 1
	CurveTypeOpModWattVar           CurveType = 2
	CurveTypeOpModVoltWatt          CurveType = 3
	CurveTypeOpModHFRTMustTrip      CurveType = 4
	CurveTypeOpModHFRTMayTrip       CurveType = 5
	CurveTypeOpModLFRTMustTrip      CurveType = 6
	CurveTypeOpModHVRTMustTrip      CurveType = 7
	CurveTypeOpModHVRTMomentary     CurveType = 8
	CurveTypeOpModHVRTMayTrip       CurveType = 9
	CurveTypeOpModLVRTMustTrip      CurveType = 10
	CurveTypeOpModLVRTMomentary     CurveType = 11
	CurveTypeOpModLVRTMayTrip       CurveType = 12
	CurveTypeOpModWattPF            CurveType = 13
	CurveTypeOpModFreqDroop         CurveType = 14
)

// DERCurve represents a piecewise-linear curve used by DER controls
// (e.g. volt-var, frequency-watt, ride-through).
type DERCurve struct {
	IdentifiedObject
	// CurveType identifies which operating mode this curve is for.
	CurveType CurveType
	// CurveData is the ordered set of data points defining the curve.
	CurveData []CurveData
	// CreationTime is when this curve was created.
	CreationTime time.Time
	// AutonomousVRefEnable enables autonomous voltage reference
	// adjustment.
	AutonomousVRefEnable *bool
	// AutonomousVRefTimeConstant is the time constant for autonomous
	// voltage reference adjustment, in hundredths of a second.
	AutonomousVRefTimeConstant *uint32
	// OpenLoopTms is the open-loop response time in hundredths of a
	// second.
	OpenLoopTms *uint16
	// RampDecTms is the ramp rate for decreasing output in hundredths
	// of a second.
	RampDecTms *uint16
	// RampIncTms is the ramp rate for increasing output in hundredths
	// of a second.
	RampIncTms *uint16
	// RampPT1Tms is the time constant for the first-order ramp in
	// hundredths of a second.
	RampPT1Tms *uint16
	// VRef is the reference voltage in hundredths of a percent of
	// nominal voltage.
	VRef *uint16
	// XMultiplier is the power of ten for the x-axis values.
	XMultiplier PowerOfTenMultiplier
	// YMultiplier is the power of ten for the y-axis values.
	YMultiplier PowerOfTenMultiplier
	// YRefType indicates whether y-axis values are percentages of
	// the reference value.
	YRefType uint8
}

// CurveData represents a single data point on a DER curve.
type CurveData struct {
	// XValue is the independent variable.
	XValue int32
	// YValue is the dependent variable.
	YValue int32
}

// --------------------------------------------------------------------
// Metering types
// --------------------------------------------------------------------

// UsagePoint represents a logical point of measurement in a metering
// hierarchy, such as a service delivery point or a sub-meter.
type UsagePoint struct {
	IdentifiedObject
	// RoleFlags indicates the roles associated with this usage point.
	RoleFlags RoleFlagsType
	// ServiceCategoryKind identifies the kind of service measured
	// (electricity, gas, water, etc.).
	ServiceCategoryKind ServiceKind
	// Status is the current status of this usage point.
	Status uint8
	// DeviceLFDI identifies the device that owns this usage point.
	DeviceLFDI HexBinary160

	// Links to related resources.
	MeterReadingListLink *ListLink
}

// ServiceKind identifies the type of service (commodity).
type ServiceKind uint8

// Service kinds.
const (
	ServiceKindElectricity        ServiceKind = 0
	ServiceKindGas                ServiceKind = 1
	ServiceKindWater              ServiceKind = 2
	ServiceKindPressure           ServiceKind = 4
	ServiceKindHeat               ServiceKind = 5
	ServiceKindCooling            ServiceKind = 6
)

// MirrorUsagePoint is a usage point on a metering mirror — a device
// that posts readings to the server, rather than the server pulling
// them.
type MirrorUsagePoint struct {
	UsagePoint
	// PostRate is the rate in seconds at which the device will post
	// readings.
	PostRate uint32
	// MirrorMeterReadings are the meter readings posted by the device.
	MirrorMeterReadings []MeterReading
}

// MeterReading is a set of readings from a single meter, grouped under
// a common reading type.
type MeterReading struct {
	IdentifiedObject
	// ReadingTypeLink links to the description of what is being read.
	ReadingTypeLink *Link
	// ReadingSetListLink links to the actual reading data.
	ReadingSetListLink *ListLink
}

// ReadingSet is a time-bounded collection of readings.
type ReadingSet struct {
	IdentifiedObject
	// TimePeriod is the interval this reading set covers.
	TimePeriod DateTimeInterval
	// ReadingListLink links to the individual readings.
	ReadingListLink *ListLink
}

// Reading is a single metered sample.
type Reading struct {
	Resource
	// LocalID is a device-assigned reading identifier.
	LocalID *HexBinary16
	// QualityFlags indicates the quality of this reading.
	QualityFlags uint16
	// TimePeriod is the interval this reading covers.
	TimePeriod DateTimeInterval
	// Value is the reading value.
	Value int64
	// SubscribableMRID is the MRID of the parent subscribable
	// resource, for subscription matching.
	SubscribableMRID *MRIDType
}

// DateTimeInterval is a time interval with a start time and a duration.
type DateTimeInterval struct {
	// Duration is the length of the interval.
	Duration time.Duration
	// Start is the beginning of the interval.
	Start time.Time
}

// ReadingType describes the unit, multiplier, kind and other metadata
// of a reading.
type ReadingType struct {
	Resource
	// AccumulationBehaviour indicates how readings accumulate.
	AccumulationBehaviour uint8
	// CommodityKind identifies the commodity being measured.
	CommodityKind uint8
	// DataQualifier describes the data qualifier.
	DataQualifier uint8
	// FlowDirection indicates the direction of flow.
	FlowDirection FlowDirection
	// IntervalLength is the nominal interval length in seconds.
	IntervalLength uint32
	// Kind identifies the measurement kind (power, energy, etc.).
	Kind uint8
	// Phase identifies the phase of measurement.
	Phase uint8
	// PowerOfTenMultiplier is the multiplier for the reading value.
	PowerOfTenMultiplier PowerOfTenMultiplier
	// UOM is the unit of measure.
	UOM UnitOfMeasure
}

// FlowDirection indicates the direction of energy flow.
type FlowDirection uint8

// Flow direction values.
const (
	FlowDirectionNA      FlowDirection = 0
	FlowDirectionForward FlowDirection = 1
	FlowDirectionReverse FlowDirection = 19
)

// UnitOfMeasure identifies a physical unit.
type UnitOfMeasure uint8

// Common units of measure used by IEEE 2030.5.
const (
	UOMNotApplicable    UnitOfMeasure = 0
	UOMWattHours        UnitOfMeasure = 72
	UOMWatts            UnitOfMeasure = 38
	UOMVarHours         UnitOfMeasure = 73
	UOMVar              UnitOfMeasure = 63
	UOMVAHours          UnitOfMeasure = 71
	UOMVA               UnitOfMeasure = 61
	UOMVolts            UnitOfMeasure = 29
	UOMAmperes          UnitOfMeasure = 5
	UOMHertz            UnitOfMeasure = 33
	UOMPowerFactor      UnitOfMeasure = 65
	UOMDegreeCelsius    UnitOfMeasure = 23
	UOMPercent          UnitOfMeasure = 37
	UOMAmpereSqHours    UnitOfMeasure = 106
)

// --------------------------------------------------------------------
// Time
// --------------------------------------------------------------------

// TimeResource is the server's current time, provided so that devices
// with inaccurate clocks can synchronize.
type TimeResource struct {
	Resource
	// CurrentTime is the server's current time.
	CurrentTime time.Time
	// DSTEndTime is the end of daylight saving time.
	DSTEndTime time.Time
	// DSTOffset is the offset applied during daylight saving time.
	DSTOffset time.Duration
	// DSTStartTime is the start of daylight saving time.
	DSTStartTime time.Time
	// LocalTime is the local time at the server, if known.
	LocalTime *time.Time
	// Quality indicates the quality of the time source.
	// 3 = GPS, 4 = NTP, 5 = no source, 6 = hand-set, 7 = unknown.
	Quality uint8
	// TZOffset is the UTC offset of the server's local time zone.
	TZOffset time.Duration
}

// TimeQuality constants.
const (
	TimeQualityGPS     uint8 = 3
	TimeQualityNTP     uint8 = 4
	TimeQualityNoSync  uint8 = 5
	TimeQualityHandSet uint8 = 6
	TimeQualityUnknown uint8 = 7
)

// --------------------------------------------------------------------
// Response types
// --------------------------------------------------------------------

// ResponseRequired is a bitmask indicating which responses the server
// requires from a device for an event or control.
type ResponseRequired uint8

// ResponseRequired flags.
const (
	// ResponseRequiredMessageReceived requests an acknowledgement
	// that the message was received.
	ResponseRequiredMessageReceived ResponseRequired = 1 << 0
	// ResponseRequiredSpecificResponse requests a specific response
	// (event started, event completed, etc.).
	ResponseRequiredSpecificResponse ResponseRequired = 1 << 1
	// ResponseRequiredResponseRequired requests that a response is
	// sent whether the event is applied or not.
	ResponseRequiredResponseRequired ResponseRequired = 1 << 2
)

// CreatedStatus is the status returned by a device in response to an
// event or control.
type CreatedStatus uint8

// CreatedStatus values.
const (
	CreatedStatusOptOut              CreatedStatus = 0
	CreatedStatusOptIn               CreatedStatus = 1
	CreatedStatusEventReceived       CreatedStatus = 2
	CreatedStatusEventStarted        CreatedStatus = 3
	CreatedStatusEventCompleted      CreatedStatus = 4
	CreatedStatusEventOptOut         CreatedStatus = 5
	CreatedStatusEventAborted        CreatedStatus = 6
	CreatedStatusEventSuperseded     CreatedStatus = 7
	CreatedStatusEventPartialOptOut  CreatedStatus = 8
	CreatedStatusEventCompletedNoUse CreatedStatus = 9
	CreatedStatusEventAbnormalCond   CreatedStatus = 10
	CreatedStatusOverheat            CreatedStatus = 11
)

// Response is a device's reply to a server event or control.
type Response struct {
	Resource
	// EndDeviceLFDI identifies the responding device.
	EndDeviceLFDI HexBinary160
	// Status is the device's response to the event.
	Status CreatedStatus
	// CreatedDateTime is when this response was created.
	CreatedDateTime time.Time
	// Subject is the MRID of the event or control being responded to.
	Subject MRIDType
}

// --------------------------------------------------------------------
// Subscription and notification types
// --------------------------------------------------------------------

// SubscriptionBase identifies a subscription to changes of a resource.
type SubscriptionBase struct {
	Resource
	// SubscribedResource is the URI of the resource being subscribed
	// to.
	SubscribedResource string
}

// Subscription extends SubscriptionBase with delivery parameters.
type Subscription struct {
	SubscriptionBase
	// Condition indicates what triggers a notification
	// (0 = change, 1 = periodic).
	Condition uint8
	// Encoding indicates the notification encoding
	// (0 = XML, 1 = EXI).
	Encoding uint8
	// Level is the depth of resource to include in notifications.
	Level string
	// Limit is the maximum number of items to include per
	// notification.
	Limit uint32
	// NotificationURI is the URI to which notifications are POSTed.
	NotificationURI string
}

// Notification is a push notification sent by the server to a
// subscribed device.
type Notification struct {
	SubscriptionBase
	// NewResourceURI is the URI of the resource that changed.
	NewResourceURI string
	// Status is the HTTP status code of the notification.
	Status uint16
	// SubscribedResource is the URI that was subscribed to.
	SubscribedResource string
}
