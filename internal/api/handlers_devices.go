package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"jukem/internal/audio"
	"jukem/internal/store"
)

// DeviceView is one output as the UI shows it.
type DeviceView struct {
	audio.Device
	Key      string `json:"key" doc:"Stable key for per-device settings"`
	Selected bool   `json:"selected"`
}

// DevicesBody lists the outputs and the saved selection.
type DevicesBody struct {
	Devices     []DeviceView    `json:"devices"`
	Saved       *audio.Identity `json:"saved,omitempty" doc:"The saved selection, present or not"`
	Present     bool            `json:"present" doc:"True when the saved selection is present"`
	MatchRule   string          `json:"match_rule,omitempty" enum:"serial,port,card_id" doc:"How the saved selection was matched"`
	SysReadable bool            `json:"sys_readable" doc:"False when /sys is not readable and only the card ID identifies devices"`
}

type devicesOutput struct {
	Body DevicesBody
}

func (s *Server) registerDevices(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-devices", Method: http.MethodGet, Path: "/devices", Tags: []string{"devices"},
		Summary: "Detected outputs with identity and presence",
	}, func(ctx context.Context, in *zoneInput) (*devicesOutput, error) {
		return &devicesOutput{Body: s.devices()}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "rescan-devices", Method: http.MethodPost, Path: "/devices/rescan", Tags: []string{"devices"},
		Summary: "Rescan the outputs now",
	}, func(ctx context.Context, in *zoneInput) (*devicesOutput, error) {
		if err := s.opts.Devices.Rescan(ctx); err != nil {
			return nil, huma.Error502BadGateway("device scan failed: " + err.Error())
		}
		return &devicesOutput{Body: s.devices()}, nil
	})

	type selectInput struct {
		zoneInput
		Body struct {
			Key string `json:"key" doc:"Key of a present device from GET /devices"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "select-device", Method: http.MethodPut, Path: "/devices/default", Tags: []string{"devices"},
		Summary: "Select the output",
	}, func(ctx context.Context, in *selectInput) (*devicesOutput, error) {
		found, err := s.presentDevice(in.Body.Key)
		if err != nil {
			return nil, err
		}
		id := found.Identity
		if err := s.opts.SelectOutput(ctx, &id); err != nil {
			return nil, err
		}
		return &devicesOutput{Body: s.devices()}, nil
	})

	type mixerInput struct {
		Key string `path:"key"`
	}
	type mixerOutput struct {
		Body struct {
			Controls   []audio.MixerControl `json:"controls"`
			Remembered *store.DeviceMixer   `json:"remembered,omitempty"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "get-mixer", Method: http.MethodGet, Path: "/devices/{key}/mixer", Tags: []string{"devices"},
		Summary: "Hardware playback controls of a present device",
	}, func(ctx context.Context, in *mixerInput) (*mixerOutput, error) {
		d, err := s.presentDevice(in.Key)
		if err != nil {
			return nil, err
		}
		controls, err := s.opts.Mixer.Controls(ctx, d.CardIndex)
		if err != nil {
			return nil, huma.Error502BadGateway(err.Error())
		}
		out := &mixerOutput{}
		out.Body.Controls = controls
		if m, ok, err := s.store.GetDeviceMixer(ctx, in.Key); err == nil && ok {
			out.Body.Remembered = &m
		}
		return out, nil
	})

	type setMixerInput struct {
		Key  string `path:"key"`
		Body struct {
			Control string `json:"control" minLength:"1"`
			Level   int    `json:"level" minimum:"0" maximum:"100"`
			Reapply bool   `json:"reapply" doc:"Apply again each time MPD starts"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "set-mixer", Method: http.MethodPut, Path: "/devices/{key}/mixer", Tags: []string{"devices"},
		Summary: "Set a hardware control level once, and optionally remember it",
	}, func(ctx context.Context, in *setMixerInput) (*mixerOutput, error) {
		d, err := s.presentDevice(in.Key)
		if err != nil {
			return nil, err
		}
		if err := s.opts.Mixer.Set(ctx, d.CardIndex, in.Body.Control, in.Body.Level); err != nil {
			return nil, huma.Error502BadGateway(err.Error())
		}
		m := store.DeviceMixer{DeviceKey: in.Key, Control: in.Body.Control, Level: in.Body.Level, Reapply: in.Body.Reapply}
		if in.Body.Reapply {
			err = s.store.SetDeviceMixer(ctx, m)
		} else {
			err = s.store.DeleteDeviceMixer(ctx, in.Key)
		}
		if err != nil {
			return nil, err
		}
		controls, err := s.opts.Mixer.Controls(ctx, d.CardIndex)
		if err != nil {
			return nil, huma.Error502BadGateway(err.Error())
		}
		out := &mixerOutput{}
		out.Body.Controls = controls
		if in.Body.Reapply {
			out.Body.Remembered = &m
		}
		return out, nil
	})
}

func (s *Server) presentDevice(key string) (*audio.Device, error) {
	snap := s.opts.Devices.Snapshot()
	for i := range snap.Devices {
		if snap.Devices[i].Key() == key {
			return &snap.Devices[i], nil
		}
	}
	return nil, huma.Error404NotFound("no present device has that key")
}

func (s *Server) devices() DevicesBody {
	snap := s.opts.Devices.Snapshot()
	showAll := s.opts.Settings().ShowAllDevices
	body := DevicesBody{Devices: []DeviceView{}, Saved: snap.Saved, Present: snap.Selected != nil, MatchRule: snap.MatchRule, SysReadable: snap.SysReadable}
	for _, d := range snap.Devices {
		if d.Hidden && !showAll {
			continue
		}
		body.Devices = append(body.Devices, DeviceView{
			Device:   d,
			Key:      d.Key(),
			Selected: snap.Selected != nil && snap.Selected.Key() == d.Key(),
		})
	}
	return body
}
