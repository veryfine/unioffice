// Copyright 2017 Baliance. All rights reserved.
//
// Use of this source code is governed by the terms of the Affero GNU General
// Public License version 3.0 as published by the Free Software Foundation and
// appearing in the file LICENSE included in the packaging of this file. A
// commercial license can be purchased by contacting sales@baliance.com.

package spreadsheet

import (
	"fmt"
	"log"
	"sort"
	"strings"

	"baliance.com/gooxml"
	"baliance.com/gooxml/common"
	sml "baliance.com/gooxml/schema/schemas.openxmlformats.org/spreadsheetml"
)

// Sheet is a single sheet within a workbook.
type Sheet struct {
	w   *Workbook
	cts *sml.CT_Sheet
	x   *sml.Worksheet
}

// X returns the inner wrapped XML type.
func (s Sheet) X() *sml.Worksheet {
	return s.x
}

// Row will return a row with a given row number, creating a new row if
// necessary.
func (s Sheet) Row(rowNum uint32) Row {
	// see if the row exists
	for _, r := range s.x.SheetData.Row {
		if r.RAttr != nil && *r.RAttr == rowNum {
			return Row{s.w, s.x, r}
		}
	}
	// create a new row
	return s.AddNumberedRow(rowNum)
}

// Cell creates or returns a cell given a cell reference of the form 'A10'
func (s Sheet) Cell(cellRef string) Cell {
	col, row, err := ParseCellReference(cellRef)
	if err != nil {
		log.Printf("error parsing cell reference: %s", err)
		return s.AddRow().AddCell()
	}
	return s.Row(row).Cell(col)
}

// AddNumberedRow adds a row with a given row number.  If you reuse a row number
// the resulting file will fail validation and fail to open in Office programs. Use
// Row instead which creates a new row or returns an existing row.
func (s Sheet) AddNumberedRow(rowNum uint32) Row {
	r := sml.NewCT_Row()
	r.RAttr = gooxml.Uint32(rowNum)
	s.x.SheetData.Row = append(s.x.SheetData.Row, r)

	// Excel wants the rows to be sorted
	sort.Slice(s.x.SheetData.Row, func(i, j int) bool {
		l := s.x.SheetData.Row[i].RAttr
		r := s.x.SheetData.Row[j].RAttr
		if l == nil {
			return true
		}
		if r == nil {
			return true
		}
		return *l < *r
	})

	return Row{s.w, s.x, r}
}

// AddRow adds a new row to a sheet.  You can mix this with numbered rows,
// however it will get confusing. You should prefer to use either automatically
// numbered rows with AddRow or manually numbered rows with Row/AddNumberedRow
func (s Sheet) AddRow() Row {
	maxRowID := uint32(0)
	// find the max row number
	for _, r := range s.x.SheetData.Row {
		if r.RAttr != nil && *r.RAttr > maxRowID {
			maxRowID = *r.RAttr
		}
	}

	return s.AddNumberedRow(maxRowID + 1)
}

// Name returns the sheet name
func (s Sheet) Name() string {
	return s.cts.NameAttr
}

// SetName sets the sheet name.
func (s Sheet) SetName(name string) {
	s.cts.NameAttr = name
}

// Validate validates the sheet, returning an error if it is found to be invalid.
func (s Sheet) Validate() error {

	// check for re-used row numbers
	usedRows := map[uint32]struct{}{}
	for _, r := range s.x.SheetData.Row {
		if r.RAttr != nil {
			if _, reusedRow := usedRows[*r.RAttr]; reusedRow {
				return fmt.Errorf("'%s' reused row %d", s.Name(), *r.RAttr)
			}
			usedRows[*r.RAttr] = struct{}{}
		}
		// or re-used column labels within a row
		usedCells := map[string]struct{}{}
		for _, c := range r.C {
			if c.RAttr == nil {
				continue
			}

			if _, reusedCell := usedCells[*c.RAttr]; reusedCell {
				return fmt.Errorf("'%s' reused cell %s", s.Name(), *c.RAttr)
			}
			usedCells[*c.RAttr] = struct{}{}
		}
	}

	if len(s.Name()) > 31 {
		return fmt.Errorf("sheet name '%s' has %d characters, max length is 31", s.Name(), len(s.Name()))
	}
	if err := s.cts.Validate(); err != nil {
		return err
	}
	return s.x.Validate()
}

// ValidateWithPath validates the sheet passing path informaton for a better
// error message
func (s Sheet) ValidateWithPath(path string) error {
	return s.cts.ValidateWithPath(path)
}

// Rows returns all of the rows in a sheet.
func (s Sheet) Rows() []Row {
	ret := []Row{}
	for _, r := range s.x.SheetData.Row {
		ret = append(ret, Row{s.w, s.x, r})
	}
	return ret
}

// SetDrawing sets the worksheet drawing.  A worksheet can have a reference to a
// single drawing, but the drawing can have many charts.
func (s Sheet) SetDrawing(d Drawing) {
	var rel common.Relationships
	for i, wks := range s.w.xws {
		if wks == s.x {
			rel = s.w.xwsRels[i]
			break
		}
	}
	// add relationship from drawing to the sheet
	var drawingID string
	for i, dr := range d.wb.drawings {
		if dr == d.x {
			rel := rel.AddAutoRelationship(gooxml.DocTypeSpreadsheet, i+1, gooxml.DrawingType)
			drawingID = rel.ID()
			break
		}
	}
	s.x.Drawing = sml.NewCT_Drawing()
	s.x.Drawing.IdAttr = drawingID
}

// AddHyperlink adds a hyperlink to a sheet. Adding the hyperlink to the sheet
// and setting it on a cell is more efficient than setting hyperlinks directly
// on a cell.
func (s Sheet) AddHyperlink(url string) common.Hyperlink {
	// store the relationships so we don't need to do a lookup here?
	for i, ws := range s.w.xws {
		if ws == s.x {
			// add a hyperlink relationship in the worksheet relationships file
			return s.w.xwsRels[i].AddHyperlink(url)
		}
	}
	// should never occur
	return common.Hyperlink{}
}

// RangeReference converts a range reference of the form 'A1:A5' to 'Sheet
// 1'!$A$1:$A$5 . Renaming a sheet after calculating a range reference will
// invalidate the reference.
func (s Sheet) RangeReference(n string) string {
	sp := strings.Split(n, ":")
	fc, fr, _ := ParseCellReference(sp[0])
	from := fmt.Sprintf("$%s$%d", fc, fr)
	if len(sp) == 1 {
		return fmt.Sprintf(`'%s'!%s`, s.Name(), from)
	}
	tc, tr, _ := ParseCellReference(sp[1])
	to := fmt.Sprintf("$%s$%d", tc, tr)
	return fmt.Sprintf(`'%s'!%s:%s`, s.Name(), from, to)
}

const autoFilterName = "_xlnm._FilterDatabase"

// ClearAutoFilter removes the autofilters from the sheet.
func (s Sheet) ClearAutoFilter() {
	s.x.AutoFilter = nil
	sn := "'" + s.Name() + "'!"
	// see if we have a defined auto filter name for the sheet
	for _, dn := range s.w.DefinedNames() {
		if dn.Name() == autoFilterName {
			if strings.HasPrefix(dn.Content(), sn) {
				s.w.RemoveDefinedName(dn)
				break
			}
		}
	}
}

// SetAutoFilter creates autofilters on the sheet. These are the automatic
// filters that are common for a header row.  The RangeRef should be of the form
// "A1:C5" and cover the entire range of cells to be filtered, not just the
// header. SetAutoFilter replaces any existing auto filter on the sheet.
func (s Sheet) SetAutoFilter(rangeRef string) {
	// this should have no $ in it
	rangeRef = strings.Replace(rangeRef, "$", "", -1)

	s.x.AutoFilter = sml.NewCT_AutoFilter()
	s.x.AutoFilter.RefAttr = gooxml.String(rangeRef)
	sn := "'" + s.Name() + "'!"
	var sdn DefinedName

	// see if we already have a defined auto filter name for the sheet
	for _, dn := range s.w.DefinedNames() {
		if dn.Name() == autoFilterName {
			if strings.HasPrefix(dn.Content(), sn) {
				sdn = dn
				// name must match, but make sure rangeRef matches as well
				sdn.SetContent(s.RangeReference(rangeRef))
				break
			}
		}
	}
	// no existing name found, so add a new one
	if sdn.X() == nil {
		sdn = s.w.AddDefinedName(autoFilterName, s.RangeReference(rangeRef))
	}

	for i, ws := range s.w.xws {
		if ws == s.x {
			sdn.SetLocalSheetID(uint32(i))
		}
	}
}

// AddMergedCells merges cells within a sheet.
func (s Sheet) AddMergedCells(fromRef, toRef string) MergedCell {
	// TODO: we might need to actually create the merged cells if they don't
	// exist, but it appears to work fine on both Excel and LibreOffice just
	// creating the merged region

	if s.x.MergeCells == nil {
		s.x.MergeCells = sml.NewCT_MergeCells()
	}

	merge := sml.NewCT_MergeCell()
	merge.RefAttr = fmt.Sprintf("%s:%s", fromRef, toRef)

	s.x.MergeCells.MergeCell = append(s.x.MergeCells.MergeCell, merge)
	s.x.MergeCells.CountAttr = gooxml.Uint32(uint32(len(s.x.MergeCells.MergeCell)))
	return MergedCell{s.w, s.x, merge}
}

// MergedCells returns the merged cell regions within the sheet.
func (s Sheet) MergedCells() []MergedCell {
	if s.x.MergeCells == nil {
		return nil
	}
	ret := []MergedCell{}
	for _, c := range s.x.MergeCells.MergeCell {
		ret = append(ret, MergedCell{s.w, s.x, c})
	}
	return ret
}

// RemoveMergedCell removes merging from a cell range within a sheet.  The cells
// that made up the merged cell remain, but are no lon merged.
func (s Sheet) RemoveMergedCell(mc MergedCell) {
	for i, c := range s.x.MergeCells.MergeCell {
		if c == mc.X() {
			copy(s.x.MergeCells.MergeCell[i:], s.x.MergeCells.MergeCell[i+1:])
			s.x.MergeCells.MergeCell[len(s.x.MergeCells.MergeCell)-1] = nil
			s.x.MergeCells.MergeCell = s.x.MergeCells.MergeCell[:len(s.x.MergeCells.MergeCell)-1]
		}
	}

}
