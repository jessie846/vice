// radarscope.go
// Copyright(c) 2022 Matt Pharr, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/mmp/imgui-go/v4"
)

type RadarScopePane struct {
	ScopeName          string
	Center             Point2LL
	Range              float32
	DataBlockFormat    DataBlockFormat
	DataBlockFrequency int32
	PointSize          float32
	LineWidth          float32

	StaticDraw *StaticDrawConfig

	DrawWeather  bool
	WeatherRadar WeatherRadar

	DrawRangeRings  bool
	RangeRingRadius float32
	RangeRingCenter string

	RotationAngle float32

	AutomaticDatablockLayout bool

	MinAltitude int32
	MaxAltitude int32

	DrawVectorLine   bool
	VectorLineExtent float32
	VectorLineMode   int
	RadarTracksDrawn int32

	DrawRangeIndicators bool
	RangeIndicatorStyle int
	RangeLimits         [NumRangeTypes]RangeLimits
	rangeWarnings       map[AircraftPair]interface{}

	AutoMIT         bool
	AutoMITAirports map[string]interface{}

	CRDAEnabled bool
	CRDAConfig  CRDAConfig

	DrawCompass bool

	DatablockFontIdentifier FontIdentifier
	datablockFont           *Font
	LabelFontIdentifier     FontIdentifier
	labelFont               *Font

	linesDrawBuilder ColoredLinesDrawBuilder
	llCommandBuffer  CommandBuffer // things using lat-long coordiantes for vertices
	wcCommandBuffer  CommandBuffer // window coordinates

	acSelectedByDatablock *Aircraft

	primaryButtonDoubleClicked bool
	primaryDragStart           [2]float32
	primaryDragEnd             [2]float32

	tdScratch TextDrawBuilder

	lastRangeNotificationPlayed time.Time

	// All of the aircraft in the world, each with additional information
	// carried along in an AircraftScopeState.
	aircraft map[*Aircraft]*AircraftScopeState
	// map from legit to their ghost, if present
	ghostAircraft map[*Aircraft]*Aircraft

	pointedOutAircraft *TransientMap[*Aircraft, string]

	eventsId EventSubscriberId

	// Backwards compatibility for config.json files written before
	// StaticDrawConfig was introduced; this allows us to grab the old
	// configuration and then copy its into StaticDraw.
	//
	// TODO: remove this at some point in the future
	OldDrawEverything   bool                   `json:"DrawEverything,omitempty"`
	OldDrawRunways      bool                   `json:"DrawRunways,omitempty"`
	OldDrawRegions      bool                   `json:"DrawRegions,omitempty"`
	OldDrawLabels       bool                   `json:"DrawLabels,omitempty"`
	OldDrawLowAirways   bool                   `json:"DrawLowAirways,omitempty"`
	OldDrawHighAirways  bool                   `json:"DrawHighAirways,omitempty"`
	OldDrawVORs         bool                   `json:"DrawVORs,omitempty"`
	OldDrawVORNames     bool                   `json:"DrawVORNames,omitempty"`
	OldVORsToDraw       map[string]interface{} `json:"VORsToDraw,omitempty"`
	OldDrawNDBs         bool                   `json:"DrawNDBs,omitempty"`
	OldDrawNDBNames     bool                   `json:"DrawNDBNames,omitempty"`
	OldNDBsToDraw       map[string]interface{} `json:"NDBsToDraw,omitempty"`
	OldDrawFixes        bool                   `json:"DrawFixes,omitempty"`
	OldDrawFixNames     bool                   `json:"DrawFixNames,omitempty"`
	OldFixesToDraw      map[string]interface{} `json:"FixesToDraw,omitempty"`
	OldDrawAirports     bool                   `json:"DrawAirports,omitempty"`
	OldDrawAirportNames bool                   `json:"DrawAirportNames,omitempty"`
	OldAirportsToDraw   map[string]interface{} `json:"AirportsToDraw,omitempty"`
	OldGeoDrawSet       map[string]interface{} `json:"GeoDrawSet,omitempty"`
	OldSIDDrawSet       map[string]interface{} `json:"SIDDrawSet,omitempty"`
	OldSTARDrawSet      map[string]interface{} `json:"STARDrawSet,omitempty"`
	OldARTCCDrawSet     map[string]interface{} `json:"ARTCCDrawSet,omitempty"`
	OldARTCCLowDrawSet  map[string]interface{} `json:"ARTCCLowDrawSet,omitempty"`
	OldARTCCHighDrawSet map[string]interface{} `json:"ARTCCHighDrawSet,omitempty"`
}

const (
	RangeIndicatorRings = iota
	RangeIndicatorLine
)

type AircraftScopeState struct {
	isGhost bool

	datablockAutomaticOffset [2]float32
	datablockManualOffset    [2]float32
	datablockText            [2]string
	datablockTextCurrent     bool
	datablockBounds          Extent2D // w.r.t. lower-left corner (so (0,0) p0 always)
}

// Takes aircraft position in window coordinates
func (t *AircraftScopeState) WindowDatablockBounds(p [2]float32) Extent2D {
	db := t.datablockBounds.Offset(p)
	if t.datablockManualOffset[0] != 0 || t.datablockManualOffset[1] != 0 {
		return db.Offset(t.datablockManualOffset)
	} else {
		return db.Offset(t.datablockAutomaticOffset)
	}
}

const (
	VectorLineNM = iota
	VectorLineMinutes
)

func NewRadarScopePane(n string) *RadarScopePane {
	c := &RadarScopePane{ScopeName: n}

	c.PointSize = 3
	c.LineWidth = 1

	c.StaticDraw = NewStaticDrawConfig()

	c.Center = database.defaultCenter
	c.MinAltitude = 0
	c.MaxAltitude = 60000
	c.Range = 15
	c.DataBlockFormat = DataBlockFormatGround
	c.DataBlockFrequency = 3
	c.RadarTracksDrawn = 5

	c.aircraft = make(map[*Aircraft]*AircraftScopeState)
	c.ghostAircraft = make(map[*Aircraft]*Aircraft)
	c.pointedOutAircraft = NewTransientMap[*Aircraft, string]()

	font := GetDefaultFont()
	c.DatablockFontIdentifier = font.id
	c.datablockFont = font
	c.labelFont = font
	c.LabelFontIdentifier = font.id

	c.CRDAConfig = NewCRDAConfig()

	c.AutoMITAirports = make(map[string]interface{})

	c.eventsId = eventStream.Subscribe()

	return c
}

func (rs *RadarScopePane) Duplicate(nameAsCopy bool) Pane {
	dupe := &RadarScopePane{}
	*dupe = *rs // get the easy stuff
	if nameAsCopy {
		dupe.ScopeName += " Copy"
	}

	dupe.StaticDraw = rs.StaticDraw.Duplicate()

	dupe.rangeWarnings = DuplicateMap(rs.rangeWarnings)

	dupe.aircraft = make(map[*Aircraft]*AircraftScopeState)
	for ac, tracked := range rs.aircraft {
		// NOTE: do not copy the TextDrawBuilder over, since we'd be aliasing
		// the slices.
		dupe.aircraft[ac] = &AircraftScopeState{
			isGhost:       tracked.isGhost,
			datablockText: tracked.datablockText}
	}

	dupe.ghostAircraft = make(map[*Aircraft]*Aircraft)
	for ac, gh := range rs.ghostAircraft {
		ghost := *gh // make a copy
		dupe.ghostAircraft[ac] = &ghost
	}
	dupe.pointedOutAircraft = NewTransientMap[*Aircraft, string]()

	dupe.AutoMITAirports = DuplicateMap(rs.AutoMITAirports)

	// don't share those slices...
	dupe.llCommandBuffer = CommandBuffer{}
	dupe.wcCommandBuffer = CommandBuffer{}
	dupe.linesDrawBuilder = ColoredLinesDrawBuilder{}

	dupe.eventsId = eventStream.Subscribe()

	return dupe
}

func (rs *RadarScopePane) getScratchTextDrawBuilder() *TextDrawBuilder {
	rs.tdScratch.Reset()
	return &rs.tdScratch
}

func (rs *RadarScopePane) Activate(cs *ColorScheme) {
	// Temporary: catch unset ones from old config files
	if rs.CRDAConfig.GlideslopeLateralSpread == 0 {
		rs.CRDAConfig = NewCRDAConfig()
	}

	// Upgrade old files
	if rs.StaticDraw == nil {
		rs.StaticDraw = NewStaticDrawConfig()

		// Copy over any values set from before StaticDrawConfig was
		// introduced and then zero the old ones out; they will then not be
		// included when the config.json file is written, thanks to
		// "omitempty"...
		rs.StaticDraw.DrawEverything = rs.OldDrawEverything
		rs.OldDrawEverything = false
		rs.StaticDraw.DrawRunways = rs.OldDrawRunways
		rs.OldDrawRunways = false
		rs.StaticDraw.DrawRegions = rs.OldDrawRegions
		rs.OldDrawRegions = false
		rs.StaticDraw.DrawLabels = rs.OldDrawLabels
		rs.OldDrawLabels = false
		rs.StaticDraw.DrawLowAirways = rs.OldDrawLowAirways
		rs.OldDrawLowAirways = false
		rs.StaticDraw.DrawHighAirways = rs.OldDrawHighAirways
		rs.OldDrawHighAirways = false
		rs.StaticDraw.DrawVORs = rs.OldDrawVORs
		rs.OldDrawVORs = false
		rs.StaticDraw.DrawVORNames = rs.OldDrawVORNames
		rs.OldDrawVORNames = false
		if len(rs.OldVORsToDraw) > 0 {
			rs.StaticDraw.VORsToDraw = rs.OldVORsToDraw
			rs.OldVORsToDraw = nil
		}
		rs.StaticDraw.DrawNDBs = rs.OldDrawNDBs
		rs.OldDrawNDBs = false
		rs.StaticDraw.DrawNDBNames = rs.OldDrawNDBNames
		rs.OldDrawNDBNames = false
		if len(rs.OldNDBsToDraw) > 0 {
			rs.StaticDraw.NDBsToDraw = rs.OldNDBsToDraw
			rs.OldNDBsToDraw = nil
		}
		rs.StaticDraw.DrawFixes = rs.OldDrawFixes
		rs.OldDrawFixes = false
		rs.StaticDraw.DrawFixNames = rs.OldDrawFixNames
		rs.OldDrawFixNames = false
		if len(rs.OldFixesToDraw) > 0 {
			rs.StaticDraw.FixesToDraw = rs.OldFixesToDraw
			rs.OldFixesToDraw = nil
		}
		rs.StaticDraw.DrawAirports = rs.OldDrawAirports
		rs.OldDrawAirports = false
		rs.StaticDraw.DrawAirportNames = rs.OldDrawAirportNames
		rs.OldDrawAirportNames = false
		if len(rs.OldAirportsToDraw) > 0 {
			rs.StaticDraw.AirportsToDraw = rs.OldAirportsToDraw
			rs.OldAirportsToDraw = nil
		}
		if len(rs.OldGeoDrawSet) > 0 {
			rs.StaticDraw.GeoDrawSet = rs.OldGeoDrawSet
			rs.OldGeoDrawSet = nil
		}
		if len(rs.OldSIDDrawSet) > 0 {
			rs.StaticDraw.SIDDrawSet = rs.OldSIDDrawSet
			rs.OldSIDDrawSet = nil
		}
		if len(rs.OldSTARDrawSet) > 0 {
			rs.StaticDraw.STARDrawSet = rs.OldSTARDrawSet
			rs.OldSTARDrawSet = nil
		}
		if len(rs.OldARTCCDrawSet) > 0 {
			rs.StaticDraw.ARTCCDrawSet = rs.OldARTCCDrawSet
			rs.OldARTCCDrawSet = nil
		}
		if len(rs.OldARTCCLowDrawSet) > 0 {
			rs.StaticDraw.ARTCCLowDrawSet = rs.OldARTCCLowDrawSet
			rs.OldARTCCLowDrawSet = nil
		}
		if len(rs.OldARTCCHighDrawSet) > 0 {
			rs.StaticDraw.ARTCCHighDrawSet = rs.OldARTCCHighDrawSet
			rs.OldARTCCHighDrawSet = nil
		}
	}

	if rs.RadarTracksDrawn == 0 {
		rs.RadarTracksDrawn = 5
	}
	if rs.DataBlockFrequency == 0 {
		rs.DataBlockFrequency = 3
	}

	rs.StaticDraw.Activate()

	if rs.AutoMITAirports == nil {
		rs.AutoMITAirports = make(map[string]interface{})
	}
	if rs.pointedOutAircraft == nil {
		rs.pointedOutAircraft = NewTransientMap[*Aircraft, string]()
	}

	if rs.datablockFont = GetFont(rs.DatablockFontIdentifier); rs.datablockFont == nil {
		rs.datablockFont = GetDefaultFont()
		rs.DatablockFontIdentifier = rs.datablockFont.id
	}
	if rs.labelFont = GetFont(rs.LabelFontIdentifier); rs.labelFont == nil {
		rs.labelFont = GetDefaultFont()
		rs.LabelFontIdentifier = rs.labelFont.id
	}

	rs.eventsId = eventStream.Subscribe()

	if rs.DrawWeather {
		rs.WeatherRadar.Activate(rs.Center)
	}

	// start tracking all of the active aircraft
	rs.initializeAircraft()
}

func (rs *RadarScopePane) initializeAircraft() {
	// Reset and initialize all of these
	rs.aircraft = make(map[*Aircraft]*AircraftScopeState)
	rs.ghostAircraft = make(map[*Aircraft]*Aircraft)

	for _, ac := range server.GetAllAircraft() {
		rs.aircraft[ac] = &AircraftScopeState{}

		if rs.CRDAEnabled {
			if ghost := rs.CRDAConfig.GetGhost(ac); ghost != nil {
				rs.ghostAircraft[ac] = ghost
				rs.aircraft[ghost] = &AircraftScopeState{isGhost: true}
			}
		}
	}
}

func (rs *RadarScopePane) Deactivate() {
	rs.StaticDraw.Deactivate()

	// Drop all of them
	rs.aircraft = nil
	rs.ghostAircraft = nil

	// Free up this memory, FWIW
	rs.llCommandBuffer = CommandBuffer{}
	rs.wcCommandBuffer = CommandBuffer{}
	rs.linesDrawBuilder = ColoredLinesDrawBuilder{}

	eventStream.Unsubscribe(rs.eventsId)
	rs.eventsId = InvalidEventSubscriberId

	if rs.DrawWeather {
		rs.WeatherRadar.Deactivate()
	}
}

func (rs *RadarScopePane) Name() string { return rs.ScopeName }

func (rs *RadarScopePane) DrawUI() {
	imgui.InputText("Name", &rs.ScopeName)
	if imgui.InputIntV("Minimum altitude", &rs.MinAltitude, 100, 1000, 0 /* flags */) {
		rs.initializeAircraft()
	}
	if imgui.InputIntV("Maximum altitude", &rs.MaxAltitude, 100, 1000, 0 /* flags */) {
		rs.initializeAircraft()
	}
	if imgui.CollapsingHeader("Aircraft rendering") {
		if rs.DataBlockFormat.DrawUI() {
			for _, state := range rs.aircraft {
				state.datablockTextCurrent = false
			}
		}
		imgui.SliderIntV("Data block update frequency (seconds)", &rs.DataBlockFrequency, 1, 10, "%d", 0 /* flags */)
		imgui.SliderIntV("Tracks shown", &rs.RadarTracksDrawn, 1, 10, "%d", 0 /* flags */)
		imgui.Checkbox("Vector lines", &rs.DrawVectorLine)
		if rs.DrawVectorLine {
			imgui.SliderFloatV("Vector line extent", &rs.VectorLineExtent, 0.1, 10, "%.1f", 0)
			imgui.SameLine()
			imgui.RadioButtonInt("nm", &rs.VectorLineMode, VectorLineNM)
			imgui.SameLine()
			imgui.RadioButtonInt("minutes", &rs.VectorLineMode, VectorLineMinutes)
		}
		imgui.Checkbox("Automatic datablock layout", &rs.AutomaticDatablockLayout)
	}
	if imgui.CollapsingHeader("Scope appearance") {
		imgui.SliderFloatV("Rotation angle", &rs.RotationAngle, -90., 90., "%.0f", 0)
		imgui.SliderFloatV("Point size", &rs.PointSize, 0.1, 20., "%.0f", 0)
		imgui.SliderFloatV("Line width", &rs.LineWidth, 0.1, 10, "%.1f", 0)
		if newFont, changed := DrawFontPicker(&rs.DatablockFontIdentifier, "Datablock font"); changed {
			rs.datablockFont = newFont
		}
		if newFont, changed := DrawFontPicker(&rs.LabelFontIdentifier, "Label font"); changed {
			rs.labelFont = newFont
		}
	}
	if imgui.CollapsingHeader("Tools") {
		if imgui.Checkbox("Weather radar", &rs.DrawWeather) {
			if rs.DrawWeather {
				// Kick off a request immediately so we get an updated image.
				rs.WeatherRadar.Activate(rs.Center)
			} else {
				rs.WeatherRadar.Deactivate()
			}
		}
		if rs.DrawWeather {
			rs.WeatherRadar.DrawUI()
		}
		imgui.Checkbox("Automatic MIT lines for arrivals", &rs.AutoMIT)
		if rs.AutoMIT {
			rs.AutoMITAirports = drawAirportSelector(rs.AutoMITAirports, "Arrival airports for auto MIT")
			imgui.Separator()
		}
		imgui.Checkbox("Draw compass directions at edges", &rs.DrawCompass)
		imgui.Checkbox("Draw range rings", &rs.DrawRangeRings)
		if rs.DrawRangeRings {
			flags := imgui.InputTextFlagsCharsNoBlank | imgui.InputTextFlagsCharsUppercase
			imgui.InputTextV("Range rings center", &rs.RangeRingCenter, flags, nil)
			if _, ok := database.Locate(rs.RangeRingCenter); !ok && rs.RangeRingCenter != "" {
				imgui.Text("Center location unknown")
			}
			if rs.RangeRingRadius == 0 {
				rs.RangeRingRadius = 5 // initial default
			}
			imgui.SliderFloatV("Range ring radius", &rs.RangeRingRadius, 0.1, 20., "%.1f", 0)
			imgui.Separator()
		}
		imgui.Checkbox("Aircraft range indicators", &rs.DrawRangeIndicators)
		if rs.DrawRangeIndicators {
			imgui.Text("Indicator")
			imgui.SameLine()
			imgui.RadioButtonInt("Rings", &rs.RangeIndicatorStyle, RangeIndicatorRings)
			imgui.SameLine()
			imgui.RadioButtonInt("Lines", &rs.RangeIndicatorStyle, RangeIndicatorLine)

			if imgui.BeginTable("RangeLimits", 4) {
				for i := range rs.RangeLimits {
					rules := RangeLimitFlightRules(i).String()
					imgui.TableNextColumn()
					imgui.Text(rules)
					imgui.TableNextColumn()
					imgui.Text("Warning")
					imgui.TableNextColumn()
					imgui.SliderFloatV("Lateral (nm)##warn"+rules, &rs.RangeLimits[i].WarningLateral,
						0, 10, "%.1f", 0)
					imgui.TableNextColumn()
					imgui.InputIntV("Vertical (feet)##warn"+rules, &rs.RangeLimits[i].WarningVertical, 100, 100, 0)

					imgui.TableNextRow()
					imgui.TableNextColumn()
					imgui.TableNextColumn()
					imgui.Text("Violation")
					imgui.TableNextColumn()
					imgui.SliderFloatV("Lateral (nm)##viol"+rules, &rs.RangeLimits[i].ViolationLateral,
						0, 10, "%.1f", 0)
					imgui.TableNextColumn()
					imgui.InputIntV("Vertical (feet)##viol"+rules, &rs.RangeLimits[i].ViolationVertical, 100, 100, 0)
				}
				imgui.EndTable()
			}
			imgui.Separator()
		}

		if imgui.Checkbox("Converging runway display aid (CRDA)", &rs.CRDAEnabled) {
			rs.initializeAircraft() // this is overkill, but nbd
		}
		if rs.CRDAEnabled {
			if rs.CRDAConfig.DrawUI() {
				rs.initializeAircraft()
			}
			imgui.Separator()
		}
	}
	if imgui.CollapsingHeader("Scope contents") {
		rs.StaticDraw.DrawUI()
	}
}

func (rs *RadarScopePane) CanTakeKeyboardFocus() bool { return false }

func (rs *RadarScopePane) processEvents(es *EventStream) {
	for _, event := range es.Get(rs.eventsId) {
		switch v := event.(type) {
		case *AddedAircraftEvent:
			rs.aircraft[v.ac] = &AircraftScopeState{}
			if rs.CRDAEnabled {
				if ghost := rs.CRDAConfig.GetGhost(v.ac); ghost != nil {
					rs.ghostAircraft[v.ac] = ghost
					rs.aircraft[ghost] = &AircraftScopeState{isGhost: true}
				}
			}

		case *RemovedAircraftEvent:
			if ghost, ok := rs.ghostAircraft[v.ac]; ok {
				delete(rs.aircraft, ghost)
			}
			delete(rs.aircraft, v.ac)
			delete(rs.ghostAircraft, v.ac)

		case *ModifiedAircraftEvent:
			if rs.CRDAEnabled {
				// always start out by removing the old ghost
				if oldGhost, ok := rs.ghostAircraft[v.ac]; ok {
					delete(rs.aircraft, oldGhost)
					delete(rs.ghostAircraft, v.ac)
				}
			}

			if state, ok := rs.aircraft[v.ac]; !ok {
				rs.aircraft[v.ac] = &AircraftScopeState{}
			} else {
				state.datablockTextCurrent = false
			}

			// new ghost
			if rs.CRDAEnabled {
				if ghost := rs.CRDAConfig.GetGhost(v.ac); ghost != nil {
					rs.ghostAircraft[v.ac] = ghost
					rs.aircraft[ghost] = &AircraftScopeState{isGhost: true}
				}
			}

		case *PointOutEvent:
			rs.pointedOutAircraft.Add(v.ac, v.controller, 5*time.Second)
		}
	}
}

func (rs *RadarScopePane) Draw(ctx *PaneContext, cb *CommandBuffer) {
	rs.processEvents(ctx.events)

	transforms := GetScopeTransformations(ctx, rs.Center, rs.Range, rs.RotationAngle)
	latLongFromWindowMtx := transforms.latLongFromWindow
	windowFromLatLongMtx := transforms.windowFromLatLong

	rs.prepareForDraw(ctx, transforms)

	windowFromLatLongP := func(p Point2LL) [2]float32 {
		return mul4p(&windowFromLatLongMtx, p)
	}
	latLongFromWindowP := func(p [2]float32) Point2LL {
		return mul4p(&latLongFromWindowMtx, p)
	}
	latLongFromWindowV := func(p [2]float32) Point2LL {
		return mul4v(&latLongFromWindowMtx, p)
	}

	if rs.DrawWeather {
		rs.WeatherRadar.Draw(transforms, cb)
	}

	// Title in upper-left corner
	td := rs.getScratchTextDrawBuilder()
	height := ctx.paneExtent.Height()
	label := rs.ScopeName
	if *devmode && ctx.mouse != nil {
		mouseLatLong := latLongFromWindowP(ctx.mouse.pos)
		label += "\nMouse position: " + mouseLatLong.String()
	}
	td.AddText(label, [2]float32{float32(rs.labelFont.size) / 2, height - float32(rs.labelFont.size)/2},
		TextStyle{Font: rs.labelFont, Color: ctx.cs.Text})
	transforms.LoadWindowViewingMatrices(cb)
	td.GenerateCommands(cb)

	// Static geometry: SIDs/STARs, runways, ...
	cb.PointSize(rs.PointSize)
	cb.LineWidth(rs.LineWidth)
	rs.StaticDraw.Draw(ctx, rs.labelFont, transforms, cb)

	if rs.DrawCompass {
		p := rs.Center
		if positionConfig.selectedAircraft != nil {
			p = positionConfig.selectedAircraft.Position()
		}
		DrawCompass(p, ctx, rs.RotationAngle, rs.labelFont, transforms, cb)
	}

	if center, ok := database.Locate(rs.RangeRingCenter); ok && rs.DrawRangeRings {
		cb.LineWidth(rs.LineWidth)
		DrawRangeRings(center, rs.RangeRingRadius, ctx, transforms, cb)
	}

	rs.drawRoute(ctx, transforms, cb)

	rs.drawCRDARegions(ctx, transforms, cb)

	// Per-aircraft stuff: tracks, datablocks, vector lines, range rings, ...
	rs.drawTracks(ctx, latLongFromWindowV, windowFromLatLongP)
	rs.updateDatablockTextAndBounds(ctx, windowFromLatLongP)
	rs.layoutDatablocks(ctx, windowFromLatLongP)
	rs.drawDatablocks(ctx, windowFromLatLongP, latLongFromWindowP)
	rs.drawVectorLines(ctx, windowFromLatLongP, latLongFromWindowP)
	rs.drawRangeIndicators(ctx, transforms, cb)
	rs.drawMIT(ctx, windowFromLatLongP)
	rs.drawMeasuringLine(ctx, latLongFromWindowP)
	rs.drawHighlighted(ctx, windowFromLatLongP)

	// Mouse events last, so that the datablock bounds are current.
	rs.consumeMouseEvents(ctx, latLongFromWindowP, latLongFromWindowV, windowFromLatLongP)

	rs.linesDrawBuilder.GenerateCommands(&rs.llCommandBuffer)
	rs.llCommandBuffer.LineWidth(3 * rs.LineWidth)

	cb.Call(rs.llCommandBuffer)
	cb.Call(rs.wcCommandBuffer)
}

func (rs *RadarScopePane) prepareForDraw(ctx *PaneContext, transforms ScopeTransformations) {
	// Reset the slices so we can draw new lines and points
	rs.linesDrawBuilder.Reset()

	rs.llCommandBuffer.Reset()
	transforms.LoadLatLongViewingMatrices(&rs.llCommandBuffer)
	rs.llCommandBuffer.PointSize(rs.PointSize)
	rs.llCommandBuffer.LineWidth(rs.LineWidth)

	rs.wcCommandBuffer.Reset()
	transforms.LoadWindowViewingMatrices(&rs.wcCommandBuffer)
	rs.wcCommandBuffer.PointSize(rs.PointSize)
	rs.wcCommandBuffer.LineWidth(rs.LineWidth)
}

func (rs *RadarScopePane) drawMIT(ctx *PaneContext, windowFromLatLongP func(p Point2LL) [2]float32) {
	width, height := ctx.paneExtent.Width(), ctx.paneExtent.Height()

	annotatedLine := func(p0 Point2LL, p1 Point2LL, color RGB, text string) {
		// Center the text
		textPos := windowFromLatLongP(mid2ll(p0, p1))
		// Cull text based on center point
		if textPos[0] >= 0 && textPos[0] < width && textPos[1] >= 0 && textPos[1] < height {
			td := rs.getScratchTextDrawBuilder()
			style := TextStyle{Font: rs.labelFont, Color: color, DrawBackground: true, BackgroundColor: ctx.cs.Background}
			td.AddTextCentered(text, textPos, style)
			td.GenerateCommands(&rs.wcCommandBuffer)
		}

		rs.linesDrawBuilder.AddLine(p0, p1, color)
	}

	// Don't do AutoMIT if a sequence has been manually specified
	if rs.AutoMIT && len(positionConfig.mit) == 0 {
		inTrail := func(front Arrival, back Arrival) bool {
			dalt := back.aircraft.Altitude() - front.aircraft.Altitude()
			backHeading := back.aircraft.Heading()
			angle := headingp2ll(back.aircraft.Position(), front.aircraft.Position(),
				database.MagneticVariation)
			diff := headingDifference(backHeading, angle)

			return diff < 150 && dalt < 3000
		}

		arr := getDistanceSortedArrivals(rs.AutoMITAirports)

		for i := 1; i < len(arr); i++ {
			ac := arr[i].aircraft

			var closest Arrival
			minDist := float32(20)
			var estDist float32
			closestSet := false

			// O(n^2). #yolo
			for j := 0; j < len(arr); j++ {
				ac2 := arr[j].aircraft
				dist := nmdistance2ll(ac.Position(), ac2.Position())

				if i == j || ac2.flightPlan.arrive != ac.flightPlan.arrive {
					continue
				}

				if dist < minDist && inTrail(arr[i], arr[j]) {
					minDist = dist
					estDist = EstimatedFutureDistance(ac2, ac, 30)
					closestSet = true
					closest = arr[j]
				}
			}
			if closestSet {
				p0 := ac.Position()
				p1 := closest.aircraft.Position()

				// Having done all this work, we'll ignore the result if
				// we're drawing a range warning for this aircraft pair...
				if _, ok := rs.rangeWarnings[AircraftPair{ac, closest.aircraft}]; ok {
					continue
				}

				text := fmt.Sprintf("%.1f (%.1f) nm", minDist, estDist)
				if minDist > 5 {
					annotatedLine(p0, p1, ctx.cs.Safe, text)
				} else if minDist > 3 {
					annotatedLine(p0, p1, ctx.cs.Caution, text)
				} else {
					annotatedLine(p0, p1, ctx.cs.Error, text)
				}
			}
		}
	} else {
		for i := 1; i < len(positionConfig.mit); i++ {
			front, trailing := positionConfig.mit[i-1], positionConfig.mit[i]

			// As above, don't draw if there's a range warning for these two
			if _, ok := rs.rangeWarnings[AircraftPair{front, trailing}]; ok {
				continue
			}

			pfront, ptrailing := front.Position(), trailing.Position()
			dist := nmdistance2ll(pfront, ptrailing)
			estDist := EstimatedFutureDistance(positionConfig.mit[i-1], positionConfig.mit[i], 30)
			text := fmt.Sprintf("%.1f (%.1f) nm", dist, estDist)
			if dist > 5 {
				annotatedLine(pfront, ptrailing, ctx.cs.Safe, text)
			} else if dist > 3 {
				annotatedLine(pfront, ptrailing, ctx.cs.Caution, text)
			} else {
				annotatedLine(pfront, ptrailing, ctx.cs.Error, text)
			}
		}
	}
}

func (rs *RadarScopePane) drawTracks(ctx *PaneContext, latLongFromWindowV func(p [2]float32) Point2LL,
	windowFromLatLongP func(p Point2LL) [2]float32) {
	td := rs.getScratchTextDrawBuilder()
	var pd PointsDrawBuilder

	now := server.CurrentTime()
	for ac, state := range rs.aircraft {
		if ac.LostTrack(now) || ac.Altitude() < int(rs.MinAltitude) || ac.Altitude() > int(rs.MaxAltitude) {
			continue
		}

		color := ctx.cs.Track
		if state.isGhost {
			color = ctx.cs.GhostDataBlock
		}

		// Draw in reverse order so that if it's not moving, more recent tracks (which will have
		// more contrast with the background), will be the ones that are visible.
		for i := rs.RadarTracksDrawn; i > 0; i-- {
			// blend the track color with the background color; more
			// background further into history but only a 50/50 blend
			// at the oldest track.
			// 1e-6 addition to avoid NaN with RadarTracksDrawn == 1.
			x := float32(i-1) / (1e-6 + float32(2*(rs.RadarTracksDrawn-1))) // 0 <= x <= 0.5
			trackColor := lerpRGB(x, color, ctx.cs.Background)

			p := ac.tracks[i-1].position
			pw := windowFromLatLongP(p)

			px := float32(3) // TODO: make configurable?
			dx := latLongFromWindowV([2]float32{1, 0})
			dy := latLongFromWindowV([2]float32{0, 1})
			// Returns lat-long point w.r.t. p with a window coordinates vector (x,y) added.
			delta := func(p Point2LL, x, y float32) Point2LL {
				return add2ll(p, add2ll(scale2f(dx, x), scale2f(dy, y)))
			}

			// Draw tracks
			if ac.mode == Standby {
				pd.AddPoint(pw, trackColor)
			} else if ac.squawk == Squawk(0o1200) {
				pxb := px * .7    // a little smaller
				sc := float32(.8) // leave a little space at the corners
				rs.linesDrawBuilder.AddLine(delta(p, -sc*pxb, -pxb), delta(p, sc*pxb, -pxb), trackColor)
				rs.linesDrawBuilder.AddLine(delta(p, pxb, -sc*pxb), delta(p, pxb, sc*pxb), trackColor)
				rs.linesDrawBuilder.AddLine(delta(p, sc*pxb, pxb), delta(p, -sc*pxb, pxb), trackColor)
				rs.linesDrawBuilder.AddLine(delta(p, -pxb, sc*pxb), delta(p, -pxb, -sc*pxb), trackColor)
			} else if controller := server.GetTrackingController(ac.Callsign()); controller != "" {
				ch := "?"
				if ctrl := server.GetController(controller); ctrl != nil {
					if pos := ctrl.GetPosition(); pos != nil {
						ch = pos.scope
					}
				}
				td.AddTextCentered(ch, pw, TextStyle{Font: rs.datablockFont, Color: trackColor})
			} else {
				// diagonals
				diagPx := px * 0.707107 /* 1/sqrt(2) */
				rs.linesDrawBuilder.AddLine(delta(p, -diagPx, -diagPx), delta(p, diagPx, diagPx), trackColor)
				rs.linesDrawBuilder.AddLine(delta(p, diagPx, -diagPx), delta(p, -diagPx, diagPx), trackColor)
				// horizontal line
				rs.linesDrawBuilder.AddLine(delta(p, -px, 0), delta(p, px, 0), trackColor)
			}
		}
	}

	pd.GenerateCommands(&rs.wcCommandBuffer)
	td.GenerateCommands(&rs.wcCommandBuffer)
}

func (rs *RadarScopePane) updateDatablockTextAndBounds(ctx *PaneContext, windowFromLatLongP func(p Point2LL) [2]float32) {
	squawkCount := make(map[Squawk]int)
	for ac, state := range rs.aircraft {
		if !state.isGhost {
			squawkCount[ac.squawk]++
		}
	}
	now := server.CurrentTime()
	for ac, state := range rs.aircraft {
		if ac.LostTrack(now) || ac.Altitude() < int(rs.MinAltitude) || ac.Altitude() > int(rs.MaxAltitude) {
			continue
		}

		if !state.datablockTextCurrent {
			hopo := ""
			if controller := server.InboundHandoffController(ac.Callsign()); controller != "" {
				hopo += FontAwesomeIconArrowLeft + controller
			}
			if controller := server.OutboundHandoffController(ac.Callsign()); controller != "" {
				hopo += FontAwesomeIconArrowRight + controller
			}
			if controller, ok := rs.pointedOutAircraft.Get(ac); ok {
				hopo += FontAwesomeIconExclamationTriangle + controller
			}
			if hopo != "" {
				hopo = "\n" + hopo
			}

			state.datablockText[0] = rs.DataBlockFormat.Format(ac, squawkCount[ac.squawk] != 1, 0) + hopo
			state.datablockText[1] = rs.DataBlockFormat.Format(ac, squawkCount[ac.squawk] != 1, 1) + hopo
			state.datablockTextCurrent = true

			bx0, by0 := rs.datablockFont.BoundText(state.datablockText[0], -2)
			bx1, by1 := rs.datablockFont.BoundText(state.datablockText[1], -2)
			bx, by := max(float32(bx0), float32(bx1)), max(float32(by0), float32(by1))
			state.datablockBounds = Extent2D{p0: [2]float32{0, -by}, p1: [2]float32{bx, 0}}
		}
	}
}

// Pick a point on the edge of datablock bounds to be the one we want as
// close as possible to the track point; either take a corner or halfway
// along an edge, according to the aircraft's heading.  Don't connect on
// the right hand side since the text tends to be ragged and there's slop
// in the bounds there.
func datablockConnectP(bbox Extent2D, heading float32) ([2]float32, bool) {
	center := bbox.Center()

	heading += 15 // simplify logic for figuring out slices below
	if heading < 0 {
		heading += 360
	}
	if heading > 360 {
		heading -= 360
	}

	if heading < 30 { // northbound (30 deg slice)
		return [2]float32{bbox.p0[0], center[1]}, false
	} else if heading < 90 { // NE (60 deg slice)
		return [2]float32{bbox.p0[0], bbox.p1[1]}, true
	} else if heading < 120 { // E (30 deg slice)
		return [2]float32{center[0], bbox.p1[1]}, false
	} else if heading < 180 { // SE (90 deg slice)
		return [2]float32{bbox.p0[0], bbox.p0[1]}, true
	} else if heading < 210 { // S (30 deg slice)
		return [2]float32{bbox.p0[0], center[1]}, false
	} else if heading < 270 { // SW (60 deg slice)
		return [2]float32{bbox.p0[0], bbox.p1[1]}, true
	} else if heading < 300 { // W (30 deg slice)
		return [2]float32{center[0], bbox.p0[1]}, false
	} else { // NW (60 deg slice)
		return [2]float32{bbox.p0[0], bbox.p0[1]}, true
	}
}

func (rs *RadarScopePane) layoutDatablocks(ctx *PaneContext, windowFromLatLongP func(Point2LL) [2]float32) {
	offsetSelfOnly := func(ac *Aircraft, info *AircraftScopeState) [2]float32 {
		bbox := info.datablockBounds.Expand(5)

		// We want the heading w.r.t. the window
		heading := ac.Heading() + rs.RotationAngle
		pConnect, isCorner := datablockConnectP(bbox, heading)

		// Translate the datablock to put the (padded) connection point
		// at (0,0)
		v := scale2f(pConnect, -1)

		if !isCorner {
			// it's an edge midpoint, so add a little more slop
			v = add2f(v, scale2f(normalize2f(v), 3))
		}

		return v
	}

	now := server.CurrentTime()
	if !rs.AutomaticDatablockLayout {
		// layout just wrt our own track; ignore everyone else
		for ac, state := range rs.aircraft {
			if ac.LostTrack(now) || ac.Altitude() < int(rs.MinAltitude) || ac.Altitude() > int(rs.MaxAltitude) {
				continue
			}

			if state.datablockManualOffset[0] != 0 || state.datablockManualOffset[1] != 0 {
				state.datablockAutomaticOffset = [2]float32{0, 0}
				continue
			}

			state.datablockAutomaticOffset = offsetSelfOnly(ac, state)
		}
		return
	} else {
		// Sort them by callsign so our iteration order is consistent
		// TODO: maybe sort by the ac pointer to be more fair across airlines?
		var aircraft []*Aircraft
		width, height := ctx.paneExtent.Width(), ctx.paneExtent.Height()
		for ac := range rs.aircraft {
			if ac.LostTrack(now) || ac.Altitude() < int(rs.MinAltitude) || ac.Altitude() > int(rs.MaxAltitude) {
				continue
			}

			pw := windowFromLatLongP(ac.Position())
			// Is it on screen (or getting there?)
			if pw[0] > -100 && pw[0] < width+100 && pw[1] > -100 && pw[1] < height+100 {
				aircraft = append(aircraft, ac)
			}
		}
		sort.Slice(aircraft, func(i, j int) bool {
			return aircraft[i].Callsign() < aircraft[j].Callsign()
		})

		// TODO: expand(5) consistency, ... ?
		// Bounds of placed data blocks in window coordinates.
		// FIXME: placedBounds is slightly a misnomer...
		datablockBounds := make([]Extent2D, len(aircraft))
		placed := make([]bool, len(aircraft))

		// First pass: anyone who has a manual offset goes where they go,
		// period.
		for i, ac := range aircraft {
			state := rs.aircraft[ac]
			if state.datablockManualOffset[0] != 0 || state.datablockManualOffset[1] != 0 {
				pw := windowFromLatLongP(ac.Position())
				b := state.WindowDatablockBounds(pw).Expand(5)
				datablockBounds[i] = b
				placed[i] = true
			}
		}

		// Second pass: anyone who can be placed without interfering with
		// already-placed ones gets to be in their happy place.
		allowed := func(b Extent2D) bool {
			for i, db := range datablockBounds {
				if placed[i] && Overlaps(b, db) {
					return false
				}
			}
			return true
		}
		for i, ac := range aircraft {
			if placed[i] {
				continue
			}
			state := rs.aircraft[ac]
			offset := offsetSelfOnly(ac, state)
			// TODO: we could do this incrementally a few pixels per frame
			// even if we could go all the way. Though then we would need
			// to consider all datablocks along the path...
			netOffset := sub2f(offset, state.datablockAutomaticOffset)

			pw := windowFromLatLongP(ac.Position())
			db := state.WindowDatablockBounds(pw).Expand(5).Offset(netOffset)
			if allowed(db) {
				placed[i] = true
				datablockBounds[i] = db
				state.datablockAutomaticOffset = offset
			}
		}

		// Third pass: all of the tricky ones...
		// FIXME: temporal stability?
		for i, ac := range aircraft {
			if placed[i] {
				continue
			}
			state := rs.aircraft[ac]

			if state.datablockAutomaticOffset[0] == 0 && state.datablockAutomaticOffset[1] == 0 {
				// First time seen: start with the ideal. Otherwise
				// start with whatever we ended up with last time.
				state.datablockAutomaticOffset = offsetSelfOnly(ac, state)
			}
		}

		// Initialize current datablockBounds for all of the unplaced aircraft
		for i, ac := range aircraft {
			if placed[i] {
				continue
			}
			state := rs.aircraft[ac]

			pw := windowFromLatLongP(ac.Position())
			datablockBounds[i] = state.WindowDatablockBounds(pw).Expand(5)
		}

		// For any datablocks that would be invalid with their current
		// automatic offset, apply forces until they are ok.
		iterScale := float32(2)
		for iter := 0; iter < 20; iter++ {
			//			iterScale /= 2
			anyOverlap := false

			// Compute and apply forces to each datablock. Treat this as a
			// point repulsion/attraction problem.  Work entirely in window
			// coordinates.  Fruchterman and Reingold 91, ish...
			for i, ac := range aircraft {
				if placed[i] {
					continue
				}

				db := datablockBounds[i]

				// Repulse current aircraft datablock from other
				// datablocks.
				var force [2]float32
				for j, pb := range datablockBounds {
					if i == j || !Overlaps(db, pb) {
						continue
					}

					anyOverlap = true
					v := sub2f(db.Center(), pb.Center())
					force = add2f(force, normalize2f(v))
				}

				// TODO ? clamp, etc?
				force = scale2f(force, iterScale)
				maxlen := float32(32) // .1 * (width + height) / 2
				if length2f(force) > maxlen {
					force = scale2f(force, maxlen/length2f(force))
				}

				state := rs.aircraft[ac]
				state.datablockAutomaticOffset = add2f(state.datablockAutomaticOffset, force)
				datablockBounds[i] = db
			}

			//lg.Printf("iter %d overlap %s", iter, anyOverlap)

			if !anyOverlap {
				//lg.Printf("no overlapping after %d iters", iter)
				//				break
			}
		}

		// Double check that everyone is non-overlapping. (For loop above
		// should probably have more iterations...)
		for i, ba := range datablockBounds {
			for j, bb := range datablockBounds {
				if i != j && Overlaps(ba, bb) {
					//lg.Printf("OVERLAP! %d %d - %+v %+v", i, j, ba, bb)
				}
			}
		}

		// We know all are ok; now pull everyone in along their attraction line.
		//for iter := 0; iter < 10; iter++ {
		for {
			anyMoved := false
			for i, ac := range aircraft {
				if placed[i] {
					continue
				}

				db := datablockBounds[i]
				// And attract our own datablock to the aircraft position.
				state := rs.aircraft[ac]
				goBack := sub2f(offsetSelfOnly(ac, state), state.datablockAutomaticOffset)
				if length2f(goBack) < 1 {
					continue
				}
				force := normalize2f(goBack)

				allowed := func(idx int, b Extent2D) bool {
					for i, db := range datablockBounds {
						if i != idx && Overlaps(b, db) {
							return false
						}
					}
					return true
				}

				dbMoved := db.Offset(force)
				if allowed(i, dbMoved) {
					anyMoved = true
					datablockBounds[i] = dbMoved
					state.datablockAutomaticOffset = add2f(state.datablockAutomaticOffset, force)
				}
			}
			if !anyMoved {
				break
			}
		}
	}
}

func (rs *RadarScopePane) datablockColor(ac *Aircraft, cs *ColorScheme) RGB {
	// This is not super efficient, but let's assume there aren't tons of ghost aircraft...
	for _, ghost := range rs.ghostAircraft {
		if ac == ghost {
			return cs.GhostDataBlock
		}
	}

	if positionConfig.selectedAircraft == ac {
		return cs.SelectedDataBlock
	}

	callsign := ac.Callsign()
	if server.InboundHandoffController(callsign) != "" {
		return cs.HandingOffDataBlock
	}
	if server.OutboundHandoffController(callsign) != "" {
		return cs.HandingOffDataBlock
	}

	controller := server.GetTrackingController(callsign)
	if controller != "" && controller == server.Callsign() {
		return cs.TrackedDataBlock
	}

	return cs.UntrackedDataBlock
}

func (rs *RadarScopePane) drawDatablocks(ctx *PaneContext, windowFromLatLongP func(p Point2LL) [2]float32,
	latLongFromWindowP func(p [2]float32) Point2LL) {
	width, height := ctx.paneExtent.Width(), ctx.paneExtent.Height()
	paneBounds := Extent2D{p0: [2]float32{0, 0}, p1: [2]float32{width, height}}

	// Sort the aircraft so that they are always drawn in the same order
	// (go's map iterator randomization otherwise randomizes the order,
	// which can cause shimmering when datablocks overlap (especially if
	// one is selected). We'll go with alphabetical by callsign, with the
	// selected aircraft, if any, always drawn last.
	aircraft := SortedMapKeysPred(rs.aircraft, func(a **Aircraft, b **Aircraft) bool {
		asel := *a == positionConfig.selectedAircraft
		bsel := *b == positionConfig.selectedAircraft
		if asel == bsel {
			// This is effectively that neither is selected; alphabetical
			return (*a).Callsign() < (*b).Callsign()
		} else {
			// Otherwise one of the two is; we want the selected one at the
			// end.
			return bsel
		}
	})
	td := rs.getScratchTextDrawBuilder()
	now := server.CurrentTime()
	actualNow := time.Now()
	for _, ac := range aircraft {
		if ac.LostTrack(now) || ac.Altitude() < int(rs.MinAltitude) || ac.Altitude() > int(rs.MaxAltitude) {
			continue
		}

		pac := windowFromLatLongP(ac.Position())
		state := rs.aircraft[ac]
		bbox := state.WindowDatablockBounds(pac)

		if !Overlaps(paneBounds, bbox) {
			continue
		}

		color := rs.datablockColor(ac, ctx.cs)

		// Draw characters starting at the upper left.
		flashCycle := (actualNow.Second() / int(rs.DataBlockFrequency)) & 1
		td.AddText(state.datablockText[flashCycle], [2]float32{bbox.p0[0] + 1, bbox.p1[1] - 1},
			TextStyle{Font: rs.datablockFont, Color: ctx.cs.Background, LineSpacing: -2})
		td.AddText(state.datablockText[flashCycle], [2]float32{bbox.p0[0], bbox.p1[1]},
			TextStyle{Font: rs.datablockFont, Color: color, LineSpacing: -2})

		// visualize bounds
		if false {
			var ld ColoredLinesDrawBuilder
			bx, by := rs.datablockFont.BoundText(state.datablockText[0], -2)
			ld.AddPolyline([2]float32{bbox.p0[0], bbox.p1[1]}, RGB{1, 0, 0},
				[][2]float32{[2]float32{float32(bx), 0},
					[2]float32{float32(bx), float32(-by)},
					[2]float32{float32(0), float32(-by)},
					[2]float32{float32(0), float32(0)}})
			ld.GenerateCommands(&rs.wcCommandBuffer)
		}

		drawLine := rs.DataBlockFormat != DataBlockFormatNone

		// quantized clamp
		qclamp := func(x, a, b float32) float32 {
			if x < a {
				return a
			} else if x > b {
				return b
			}
			return (a + b) / 2
		}
		// the datablock has been moved, so let's make clear what it's connected to
		if drawLine {
			var ex, ey float32
			wp := windowFromLatLongP(ac.Position())
			if wp[1] < bbox.p0[1] {
				ex = qclamp(wp[0], bbox.p0[0], bbox.p1[0])
				ey = bbox.p0[1]
			} else if wp[1] > bbox.p1[1] {
				ex = qclamp(wp[0], bbox.p0[0], bbox.p1[0])
				ey = bbox.p1[1]
			} else if wp[0] < bbox.p0[0] {
				ex = bbox.p0[0]
				ey = qclamp(wp[1], bbox.p0[1], bbox.p1[1])
			} else if wp[0] > bbox.p1[0] {
				ex = bbox.p1[0]
				ey = qclamp(wp[1], bbox.p0[1], bbox.p1[1])
			} else {
				// inside...
				drawLine = false
			}

			if drawLine {
				color := rs.datablockColor(ac, ctx.cs)
				pll := latLongFromWindowP([2]float32{ex, ey})
				rs.linesDrawBuilder.AddLine(ac.Position(), [2]float32{pll[0], pll[1]}, color)
			}
		}
	}
	td.GenerateCommands(&rs.wcCommandBuffer)
}

func (rs *RadarScopePane) drawVectorLines(ctx *PaneContext, windowFromLatLongP func(Point2LL) [2]float32,
	latLongFromWindowP func([2]float32) Point2LL) {
	if !rs.DrawVectorLine {
		return
	}

	now := server.CurrentTime()
	for ac, state := range rs.aircraft {
		if ac.LostTrack(now) || ac.Altitude() < int(rs.MinAltitude) || ac.Altitude() > int(rs.MaxAltitude) {
			continue
		}

		// Don't draw junk for the first few tracks until we have a good
		// sense of the heading.
		if ac.HaveHeading() {
			start, end := ac.Position(), rs.vectorLineEnd(ac)
			sw, ew := windowFromLatLongP(start), windowFromLatLongP(end)
			v := sub2f(ew, sw)
			if length2f(v) > 12 {
				// advance the start by 6px to make room for the track blip
				sw = add2f(sw, scale2f(normalize2f(v), 6))
				// It's a little annoying to be xforming back to latlong at
				// this point...
				start = latLongFromWindowP(sw)
			}
			if state.isGhost {
				rs.linesDrawBuilder.AddLine(start, end, ctx.cs.GhostDataBlock)
			} else {
				rs.linesDrawBuilder.AddLine(start, end, ctx.cs.Track)
			}
		}
	}
}

func (rs *RadarScopePane) drawRangeIndicators(ctx *PaneContext, transforms ScopeTransformations, cb *CommandBuffer) {
	if !rs.DrawRangeIndicators {
		return
	}

	now := server.CurrentTime()
	aircraft, _ := FlattenMap(FilterMap(rs.aircraft, func(ac *Aircraft, state *AircraftScopeState) bool {
		return !state.isGhost && !ac.LostTrack(now) && ac.Altitude() >= int(rs.MinAltitude) && ac.Altitude() <= int(rs.MaxAltitude)
	}))
	warnings, violations := GetConflicts(aircraft, rs.RangeLimits)

	// Reset it each frame
	rs.rangeWarnings = make(map[AircraftPair]interface{})
	for _, w := range warnings {
		rs.rangeWarnings[AircraftPair{w.aircraft[0], w.aircraft[1]}] = nil
		rs.rangeWarnings[AircraftPair{w.aircraft[1], w.aircraft[0]}] = nil
	}
	for _, v := range violations {
		rs.rangeWarnings[AircraftPair{v.aircraft[0], v.aircraft[1]}] = nil
		rs.rangeWarnings[AircraftPair{v.aircraft[1], v.aircraft[0]}] = nil
	}

	// Audio alert
	if len(violations) > 0 && time.Since(rs.lastRangeNotificationPlayed) > 3*time.Second {
		globalConfig.AudioSettings.HandleEvent(AudioEventConflictAlert)
		rs.lastRangeNotificationPlayed = time.Now()
	}

	pixelDistanceNm := transforms.PixelDistanceNM()

	switch rs.RangeIndicatorStyle {
	case RangeIndicatorRings:
		ld := ColoredLinesDrawBuilder{}
		for _, w := range warnings {
			nsegs := 360
			p0 := transforms.WindowFromLatLongP(w.aircraft[0].Position())
			ld.AddCircle(p0, w.limits.WarningLateral/pixelDistanceNm, nsegs, ctx.cs.Caution)
			p1 := transforms.WindowFromLatLongP(w.aircraft[1].Position())
			ld.AddCircle(p1, w.limits.WarningLateral/pixelDistanceNm, nsegs, ctx.cs.Caution)
		}
		for _, v := range violations {
			nsegs := 360
			p0 := transforms.WindowFromLatLongP(v.aircraft[0].Position())
			ld.AddCircle(p0, v.limits.ViolationLateral/pixelDistanceNm, nsegs, ctx.cs.Error)
			p1 := transforms.WindowFromLatLongP(v.aircraft[1].Position())
			ld.AddCircle(p1, v.limits.ViolationLateral/pixelDistanceNm, nsegs, ctx.cs.Error)
		}

		transforms.LoadWindowViewingMatrices(cb)
		cb.LineWidth(rs.LineWidth)
		ld.GenerateCommands(cb)

	case RangeIndicatorLine:
		ld := ColoredLinesDrawBuilder{}
		td := rs.getScratchTextDrawBuilder()
		annotatedLine := func(p0 Point2LL, p1 Point2LL, color RGB, text string) {
			textPos := transforms.WindowFromLatLongP(mid2ll(p0, p1))
			style := TextStyle{
				Font:            rs.labelFont,
				Color:           color,
				DrawBackground:  true,
				BackgroundColor: ctx.cs.Background}
			td.AddTextCentered(text, textPos, style)
			ld.AddLine(p0, p1, color)
		}

		rangeText := func(ac0, ac1 *Aircraft) string {
			dist := nmdistance2ll(ac0.Position(), ac1.Position())
			dalt := (abs(ac0.Altitude()-ac1.Altitude()) + 50) / 100
			return fmt.Sprintf("%.1f %d", dist, dalt)
		}

		for _, w := range warnings {
			ac0, ac1 := w.aircraft[0], w.aircraft[1]
			annotatedLine(ac0.Position(), ac1.Position(), ctx.cs.Caution, rangeText(ac0, ac1))
		}
		for _, v := range violations {
			ac0, ac1 := v.aircraft[0], v.aircraft[1]
			annotatedLine(ac0.Position(), ac1.Position(), ctx.cs.Error, rangeText(ac0, ac1))
		}

		transforms.LoadLatLongViewingMatrices(cb)
		cb.LineWidth(rs.LineWidth)
		ld.GenerateCommands(cb)
		transforms.LoadWindowViewingMatrices(cb)
		td.GenerateCommands(cb)
	}
}

func (rs *RadarScopePane) drawMeasuringLine(ctx *PaneContext, latLongFromWindowP func([2]float32) Point2LL) {
	if !rs.primaryButtonDoubleClicked {
		return
	}

	p0 := latLongFromWindowP(rs.primaryDragStart)
	p1 := latLongFromWindowP(rs.primaryDragEnd)

	// TODO: separate color for this rather than piggybacking?
	rs.linesDrawBuilder.AddLine(p0, p1, ctx.cs.SelectedDataBlock)

	// distance between the two points in nm
	dist := nmdistance2ll(p0, p1)

	// heading and reciprocal
	hdg := int(headingp2ll(p0, p1, database.MagneticVariation) + 0.5)
	if hdg == 0 {
		hdg = 360
	}
	rhdg := hdg + 180
	if rhdg > 360 {
		rhdg -= 360
	}
	label := fmt.Sprintf(" %.1f nm \n%d / %d", dist, hdg, rhdg)
	td := rs.getScratchTextDrawBuilder()
	style := TextStyle{
		Font:            rs.labelFont,
		Color:           ctx.cs.SelectedDataBlock,
		DrawBackground:  true,
		BackgroundColor: ctx.cs.Background}
	textPos := mid2f(rs.primaryDragStart, rs.primaryDragEnd)
	td.AddTextCentered(label, textPos, style)
	td.GenerateCommands(&rs.wcCommandBuffer)
}

func (rs *RadarScopePane) drawHighlighted(ctx *PaneContext, windowFromLatLongP func(Point2LL) [2]float32) {
	remaining := time.Until(positionConfig.highlightedLocationEndTime)
	if remaining < 0 {
		return
	}

	color := ctx.cs.Error
	fade := 1.5
	if sec := remaining.Seconds(); sec < fade {
		x := float32(sec / fade)
		color = lerpRGB(x, ctx.cs.Background, color)
	}

	p := windowFromLatLongP(positionConfig.highlightedLocation)
	radius := float32(10) // 10 pixel radius
	lines := ColoredLinesDrawBuilder{}
	lines.AddCircle(p, radius, 360, color)

	rs.wcCommandBuffer.LineWidth(3 * rs.LineWidth)
	lines.GenerateCommands(&rs.wcCommandBuffer)
}

func (rs *RadarScopePane) drawRoute(ctx *PaneContext, transforms ScopeTransformations, cb *CommandBuffer) {
	remaining := time.Until(positionConfig.drawnRouteEndTime)
	if remaining < 0 {
		return
	}

	color := ctx.cs.Error
	fade := 1.5
	if sec := remaining.Seconds(); sec < fade {
		x := float32(sec / fade)
		color = lerpRGB(x, ctx.cs.Background, color)
	}

	ld := ColoredLinesDrawBuilder{}
	var pPrev Point2LL
	for _, waypoint := range strings.Split(positionConfig.drawnRoute, " ") {
		if p, ok := database.Locate(waypoint); !ok {
			// no worries; most likely it's a SID, STAR, or airway..
		} else {
			if !pPrev.IsZero() {
				ld.AddLine(pPrev, p, color)
			}
			pPrev = p
		}
	}

	transforms.LoadLatLongViewingMatrices(cb)
	cb.LineWidth(3 * rs.LineWidth)
	ld.GenerateCommands(cb)
}

func (rs *RadarScopePane) consumeMouseEvents(ctx *PaneContext, latLongFromWindowP func([2]float32) Point2LL,
	latLongFromWindowV func([2]float32) Point2LL, windowFromLatLongP func(Point2LL) [2]float32) {
	if ctx.mouse == nil {
		return
	}

	// Handle dragging the scope center
	if ctx.mouse.dragging[mouseButtonPrimary] && rs.primaryButtonDoubleClicked {
		rs.primaryDragEnd = add2f(rs.primaryDragEnd, ctx.mouse.dragDelta)
	} else if rs.primaryButtonDoubleClicked {
		rs.primaryButtonDoubleClicked = false
	}
	if ctx.mouse.dragging[mouseButtonSecondary] {
		delta := ctx.mouse.dragDelta
		if delta[0] != 0 || delta[1] != 0 {
			deltaLL := latLongFromWindowV(delta)
			rs.Center = sub2f(rs.Center, deltaLL)
			if rs.DrawWeather {
				rs.WeatherRadar.UpdateCenter(rs.Center)
			}
		}
	}

	// Consume mouse wheel
	if ctx.mouse.wheel[1] != 0 {
		scale := pow(1.05, ctx.mouse.wheel[1])

		// We want to zoom in centered at the mouse position; this affects
		// the scope center after the zoom, so we'll find the
		// transformation that gives the new center position.
		mouseLL := latLongFromWindowP(ctx.mouse.pos)
		centerTransform := mgl32.Translate3D(-mouseLL[0], -mouseLL[1], 0)
		centerTransform = mgl32.Scale3D(scale, scale, 1).Mul4(centerTransform)
		centerTransform = mgl32.Translate3D(mouseLL[0], mouseLL[1], 0).Mul4(centerTransform)
		rs.Center = mul4p(&centerTransform, rs.Center)
		if rs.DrawWeather {
			rs.WeatherRadar.UpdateCenter(rs.Center)
		}

		rs.Range *= scale
	}

	if rs.acSelectedByDatablock != nil {
		if ctx.mouse.dragging[mouseButtonPrimary] {
			ac := rs.acSelectedByDatablock
			state := rs.aircraft[ac]
			state.datablockManualOffset =
				add2f(state.datablockAutomaticOffset, add2f(state.datablockManualOffset, ctx.mouse.dragDelta))
			state.datablockAutomaticOffset = [2]float32{0, 0}
		} else {
			rs.acSelectedByDatablock = nil
		}
	}

	// Update selected aircraft
	if ctx.mouse.doubleClicked[mouseButtonPrimary] {
		rs.primaryButtonDoubleClicked = true
		rs.primaryDragStart = ctx.mouse.pos
		rs.primaryDragEnd = rs.primaryDragStart
	}
	if ctx.mouse.clicked[mouseButtonPrimary] {
		var clickedAircraft *Aircraft
		clickedDistance := float32(20) // in pixels; don't consider anything farther away

		// Allow clicking on any track
		for ac := range rs.aircraft {
			pw := windowFromLatLongP(ac.Position())
			dist := distance2f(pw, ctx.mouse.pos)

			if dist < clickedDistance {
				clickedAircraft = ac
				clickedDistance = dist
			}
		}

		// And now check and see if we clicked on a datablock (TODO: check for held)
		now := server.CurrentTime()
		for ac, state := range rs.aircraft {
			if ac.LostTrack(now) || ac.Altitude() < int(rs.MinAltitude) || ac.Altitude() > int(rs.MaxAltitude) {
				continue
			}

			pw := windowFromLatLongP(ac.Position())
			db := state.WindowDatablockBounds(pw)
			if db.Inside(ctx.mouse.pos) {
				rs.acSelectedByDatablock = ac
				clickedAircraft = ac
				break
			}
		}

		if clickedAircraft != nil {
			eventStream.Post(&SelectedAircraftEvent{ac: clickedAircraft})
		}
	}
}

func (rs *RadarScopePane) vectorLineEnd(ac *Aircraft) Point2LL {
	switch rs.VectorLineMode {
	case VectorLineNM:
		// we want the vector length to be l=rs.VectorLineExtent.
		// we have a heading vector (hx, hy) and scale factors (sx, sy) due to lat/long compression.
		// we want a t to scale the heading by to have that length.
		// solve (sx t hx)^2 + (hy t hy)^2 = l^2 ->
		// t = sqrt(l^2 / ((sx hx)^2 + (sy hy)^2)
		h := ac.HeadingVector()
		t := sqrt(sqr(rs.VectorLineExtent) / (sqr(h[1]*database.NmPerLatitude) + sqr(h[0]*database.NmPerLongitude)))
		return add2ll(ac.Position(), scale2ll(h, t))

	case VectorLineMinutes:
		// HeadingVector() comes back scaled for one minute in the future.
		vectorEnd := scale2ll(ac.HeadingVector(), rs.VectorLineExtent)
		return add2ll(ac.Position(), vectorEnd)

	default:
		lg.Printf("unexpected vector line mode: %d", rs.VectorLineMode)
		return Point2LL{}
	}
}

func (rs *RadarScopePane) drawCRDARegions(ctx *PaneContext, transforms ScopeTransformations, cb *CommandBuffer) {
	if !rs.CRDAConfig.ShowCRDARegions {
		return
	}

	transforms.LoadLatLongViewingMatrices(cb)

	// Find the intersection of the two runways.  Work in nm space, not lat-long
	if true {
		src, dst := rs.CRDAConfig.getRunways()
		if src != nil && dst != nil {
			p, ok := runwayIntersection(src, dst)
			if !ok {
				lg.Printf("no intersection between runways?!")
			}
			//		rs.linesDrawBuilder.AddLine(src.threshold, src.end, RGB{0, 1, 0})
			//		rs.linesDrawBuilder.AddLine(dst.threshold, dst.end, RGB{0, 1, 0})
			var pd PointsDrawBuilder
			pd.AddPoint(p, RGB{1, 0, 0})
			pd.GenerateCommands(cb)
		}
	}

	src, _ := rs.CRDAConfig.getRunways()
	if src == nil {
		return
	}

	// we have the runway heading, but we want to go the opposite direction
	// and then +/- HeadingTolerance.
	rota := src.heading + 180 - rs.CRDAConfig.GlideslopeLateralSpread - database.MagneticVariation
	rotb := src.heading + 180 + rs.CRDAConfig.GlideslopeLateralSpread - database.MagneticVariation

	// Lay out the vectors in nm space, not lat-long
	sina, cosa := sin(radians(rota)), cos(radians(rota))
	va := [2]float32{sina, cosa}
	dist := float32(25)
	va = scale2f(va, dist)

	sinb, cosb := sin(radians(rotb)), cos(radians(rotb))
	vb := scale2f([2]float32{sinb, cosb}, dist)

	// Over to lat-long to draw the lines
	vall, vbll := nm2ll(va), nm2ll(vb)
	ld := ColoredLinesDrawBuilder{}
	ld.AddLine(src.threshold, add2ll(src.threshold, vall), ctx.cs.Caution)
	ld.AddLine(src.threshold, add2ll(src.threshold, vbll), ctx.cs.Caution)
	ld.GenerateCommands(cb)
}
