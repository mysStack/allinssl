package dnscontrol

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"ALLinSSL/backend/internal/dnsmodel"
)

type previewReportItem struct {
	Domain            string   `json:"domain"`
	Corrections       int      `json:"corrections"`
	CorrectionDetails []string `json:"correction_details"`
	Provider          string   `json:"provider"`
	Registrar         string   `json:"registrar"`
}

func ParseReport(data []byte, zone string) (PreviewPlan, error) {
	canonicalZone, err := dnsmodel.NormalizeZone(zone)
	if err != nil || canonicalZone != zone {
		return PreviewPlan{}, ErrInvalidZone
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var items []previewReportItem
	if err := decoder.Decode(&items); err != nil {
		return PreviewPlan{}, ErrInvalidReport
	}
	if err := consumeReportEnd(decoder); err != nil {
		return PreviewPlan{}, ErrInvalidReport
	}
	if len(items) != 2 {
		return PreviewPlan{}, ErrInvalidReport
	}

	providerItem, _, ok := reportItemsForZone(items, canonicalZone)
	if !ok {
		return PreviewPlan{}, ErrInvalidReport
	}

	return PreviewPlan{
		Zone:        canonicalZone,
		Provider:    "ALIDNS",
		Corrections: providerItem.Corrections,
		Details:     append([]string(nil), providerItem.CorrectionDetails...),
	}, nil
}

func reportItemsForZone(items []previewReportItem, zone string) (previewReportItem, previewReportItem, bool) {
	var providerItem, registrarItem previewReportItem
	for _, item := range items {
		if item.Domain != zone || item.Corrections < 0 {
			return previewReportItem{}, previewReportItem{}, false
		}
		if strings.EqualFold(item.Provider, "alidns") && item.Registrar == "" && providerItem.Provider == "" {
			providerItem = item
			continue
		}
		if item.Provider == "" && item.Registrar == "none" && item.Corrections == 0 && len(item.CorrectionDetails) == 0 && registrarItem.Registrar == "" {
			registrarItem = item
			continue
		}
		return previewReportItem{}, previewReportItem{}, false
	}

	return providerItem, registrarItem, providerItem.Provider != "" && registrarItem.Registrar != ""
}

func consumeReportEnd(decoder *json.Decoder) error {
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return ErrInvalidReport
	}
	return nil
}
