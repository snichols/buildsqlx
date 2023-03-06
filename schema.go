package buildsqlx

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
)

// column types
const (
	TypeSerial       = "SERIAL"
	TypeBigSerial    = "BIGSERIAL"
	TypeSmallInt     = "SMALLINT"
	TypeInt          = "INTEGER"
	TypeBigInt       = "BIGINT"
	TypeBoolean      = "BOOLEAN"
	TypeText         = "TEXT"
	TypeVarchar      = "VARCHAR"
	TypeChar         = "CHAR"
	TypeDate         = "DATE"
	TypeTime         = "TIME"
	TypeDateTime     = "TIMESTAMP"
	TypeDateTimeTz   = "TIMESTAMPTZ"
	CurrentDate      = "CURRENT_DATE"
	CurrentTime      = "CURRENT_TIME"
	CurrentDateTime  = "NOW()"
	TypeDblPrecision = "DOUBLE PRECISION"
	TypeNumeric      = "NUMERIC"
	TypeTsVector     = "TSVECTOR"
	TypeTsQuery      = "TSQUERY"
	TypeJson         = "JSON"
	TypeJsonb        = "JSONB"
	TypePoint        = "POINT"
	TypePolygon      = "POLYGON"
	TypeByteArray    = "BYTEA"
)

// specific for PostgreSQL driver + SQL std
const (
	DefaultSchema  = "public"
	SemiColon      = ";"
	AlterTable     = "ALTER TABLE "
	Add            = " ADD "
	Modify         = " ALTER "
	Drop           = " DROP "
	Rename         = " RENAME "
	IfExistsExp    = " IF EXISTS "
	IfNotExistsExp = " IF NOT EXISTS "
	Concurrently   = " CONCURRENTLY "
	Constraint     = " CONSTRAINT "
)

const (
	IfExistsUndeclared = iota
	IfExists
	IfNotExists
)

type colType string

// Table is the type for operations on table schema
type Table struct {
	ifExists uint
	columns  []*column
	tblName  string
	comment  *string
}

// collection of properties for the column
type column struct {
	IsNotNull       bool
	IsPrimaryKey    bool
	IsIndex         bool
	IsIdxConcurrent bool
	IsUnique        bool
	IsDrop          bool
	IsModify        bool
	IfExists        uint
	Includes        []string
	Name            string
	RenameTo        *string
	ColumnType      colType
	Default         *string
	ForeignKey      *string
	IdxName         string
	Comment         *string
	Collation       *string
	Op              string
}

// Schema creates and/or manipulates table structure with an appropriate types/indices/comments/defaults/nulls etc
func (r *DB) Schema(tblName string, fn func(table *Table) error) (res sql.Result, err error) {
	tbl := &Table{tblName: tblName}
	err = fn(tbl) // run fn with Table struct passed to collect columns to []*column slice
	if err != nil {
		return nil, err
	}

	l := len(tbl.columns)
	if l > 0 {
		tblExists, err := r.HasTable(DefaultSchema, tblName)
		if err != nil {
			return nil, err
		}

		if tblExists { // modify tbl by adding/modifying/deleting columns/indices
			return r.modifyTable(tbl)
		}
		// create table with relative columns/indices
		return r.createTable(tbl)
	}

	return
}

// SchemaIfNotExists creates table structure if not exists with an appropriate types/indices/comments/defaults/nulls etc
func (r *DB) SchemaIfNotExists(tblName string, fn func(table *Table) error) (res sql.Result, err error) {
	tbl := &Table{tblName: tblName}
	err = fn(tbl) // run fn with Table struct passed to collect columns to []*column slice
	if err != nil {
		return nil, err
	}

	l := len(tbl.columns)
	if l > 0 {
		// create table with relative columns/indices
		tbl.ifExists = IfNotExists
		return r.createTable(tbl)
	}

	return
}

func (r *DB) createIndices(indices []string) (res sql.Result, err error) {
	for _, idx := range indices {
		if idx != "" {
			res, err = r.Sql().Exec(idx)
			if err != nil {
				return nil, err
			}
		}
	}
	return
}

func (r *DB) createComments(comments []string) (res sql.Result, err error) {
	for _, comment := range comments {
		if comment != "" {
			res, err = r.Sql().Exec(comment)
			if err != nil {
				return nil, err
			}
		}
	}
	return
}

// builds column definition
func composeColumn(col *column) string {
	return col.Name + " " + string(col.ColumnType) + buildColumnOptions(col)
}

// builds column definition
func composeAddColumn(tblName string, col *column) string {
	return columnDef(tblName, col, Add)
}

// builds column definition
func composeModifyColumn(tblName string, col *column) string {
	return columnDef(tblName, col, col.Op)
}

// builds column definition
func composeDrop(tblName string, col *column) string {
	if col.IsIndex {
		return dropIdxDef(col)
	}
	return columnDef(tblName, col, Drop)
}

// concats all definition in 1 string expression
func columnDef(tblName string, col *column, op string) (colDef string) {
	colDef = AlterTable + tblName + op + "COLUMN " + applyExistence(col.IfExists) + col.Name
	if op == Rename {
		return colDef + " TO " + *col.RenameTo
	}
	if op == Modify {
		colDef += " TYPE "
	}
	if op != Drop {
		colDef += " " + string(col.ColumnType) + buildColumnOptions(col)
	}

	return
}

func applyExistence(ifExists uint) string {
	if ifExists == IfExistsUndeclared {
		return ""
	}

	if ifExists == IfExists {
		return IfExistsExp
	}

	return IfNotExistsExp
}

func dropIdxDef(col *column) string {
	return "DROP INDEX " + applyExistence(col.IfExists) + col.IdxName
}

func buildColumnOptions(col *column) (colSchema string) {
	if col.IsPrimaryKey {
		colSchema += " PRIMARY KEY"
	}

	if col.IsNotNull {
		colSchema += " NOT NULL"
	}

	if col.Default != nil {
		colSchema += " DEFAULT " + *col.Default
	}

	if col.Collation != nil {
		colSchema += " COLLATE \"" + *col.Collation + "\""
	}
	return
}

// build index for table on particular column depending on an index type
func composeIndex(tblName string, col *column) string {
	if col.IsIndex {
		return "CREATE INDEX " + applyIdxConcurrency(col.IsIdxConcurrent) + applyExistence(col.IfExists) +
			col.IdxName + " ON " + tblName + " (" + col.Name + ")" + applyIncludes(col.Includes)
	}

	if col.IsUnique {
		return "CREATE UNIQUE INDEX " + applyIdxConcurrency(col.IsIdxConcurrent) + applyExistence(col.IfExists) +
			col.IdxName + " ON " + tblName + " (" + col.Name + ")" + applyIncludes(col.Includes)
	}

	if col.ForeignKey != nil {
		if col.IsIdxConcurrent {
			concurrentFk := ""
			words := strings.Fields(*col.ForeignKey)
			for _, word := range words {
				seq := " " + word + " "
				if word == Constraint {
					seq += " " + Concurrently + " "
				}
				concurrentFk += seq
			}

			return concurrentFk
		}

		return *col.ForeignKey
	}

	return ""
}

func applyIdxConcurrency(isIdxConcurrent bool) string {
	if isIdxConcurrent {
		return Concurrently
	}

	return ""
}

func applyIncludes(includes []string) string {
	if len(includes) > 0 {
		incFields := ""
		l := len(includes)
		for i, include := range includes {
			incFields += include
			if i < l-1 {
				incFields += ", "
			}
		}

		return fmt.Sprintf(" INCLUDE(%s)", incFields)
	}

	return ""
}

func composeComment(tblName string, col *column) string {
	if col.Comment != nil {
		return "COMMENT ON COLUMN " + tblName + "." + col.Name + " IS '" + *col.Comment + "'"
	}
	return ""
}

func (t *Table) composeTableComment() string {
	if t.comment != nil {
		return "COMMENT ON TABLE " + t.tblName + " IS '" + *t.comment + "'"
	}
	return ""
}

// Increments creates auto incremented primary key integer column
func (t *Table) Increments(colNm string) *Table {
	t.columns = append(t.columns, &column{Name: colNm, ColumnType: TypeSerial, IsPrimaryKey: true})
	return t
}

// BigIncrements creates auto incremented primary key big integer column
func (t *Table) BigIncrements(colNm string) *Table {
	t.columns = append(t.columns, &column{Name: colNm, ColumnType: TypeBigSerial, IsPrimaryKey: true})
	return t
}

// SmallInt creates small integer column
func (t *Table) SmallInt(colNm string) *Table {
	t.columns = append(t.columns, &column{Name: colNm, ColumnType: TypeSmallInt})
	return t
}

// Integer creates an integer column
func (t *Table) Integer(colNm string) *Table {
	t.columns = append(t.columns, &column{Name: colNm, ColumnType: TypeInt})
	return t
}

// BigInt creates big integer column
func (t *Table) BigInt(colNm string) *Table {
	t.columns = append(t.columns, &column{Name: colNm, ColumnType: TypeBigInt})
	return t
}

// String creates varchar(len) column
func (t *Table) String(colNm string, len uint64) *Table {
	t.columns = append(t.columns, &column{Name: colNm, ColumnType: colType(TypeVarchar + "(" + strconv.FormatUint(len, 10) + ")")})
	return t
}

// Char creates char(len) column
func (t *Table) Char(colNm string, len uint64) *Table {
	t.columns = append(t.columns, &column{Name: colNm, ColumnType: colType(TypeChar + "(" + strconv.FormatUint(len, 10) + ")")})
	return t
}

// Boolean creates boolean type column
func (t *Table) Boolean(colNm string) *Table {
	t.columns = append(t.columns, &column{Name: colNm, ColumnType: TypeBoolean})
	return t
}

// Text	creates text type column
func (t *Table) Text(colNm string) *Table {
	t.columns = append(t.columns, &column{Name: colNm, ColumnType: TypeText})
	return t
}

// DblPrecision	creates dbl precision type column
func (t *Table) DblPrecision(colNm string) *Table {
	t.columns = append(t.columns, &column{Name: colNm, ColumnType: TypeDblPrecision})
	return t
}

// Numeric creates exact, user-specified precision number
func (t *Table) Numeric(colNm string, precision, scale uint64) *Table {
	t.columns = append(t.columns, &column{Name: colNm, ColumnType: colType(TypeNumeric + "(" + strconv.FormatUint(precision, 10) + ", " + strconv.FormatUint(scale, 10) + ")")})
	return t
}

// Decimal alias for Numeric as for PostgreSQL they are the same
func (t *Table) Decimal(colNm string, precision, scale uint64) *Table {
	return t.Numeric(colNm, precision, scale)
}

// Binary creates a byte array
func (t *Table) Binary(colNm string) *Table {
	t.columns = append(t.columns, &column{Name: colNm, ColumnType: colType(TypeByteArray)})
	return t
}

// NotNull sets the last column to not null
func (t *Table) NotNull() *Table {
	t.columns[len(t.columns)-1].IsNotNull = true
	return t
}

// Collation sets the last column to specified collation
func (t *Table) Collation(coll string) *Table {
	t.columns[len(t.columns)-1].Collation = &coll
	return t
}

// Default sets the default column value
func (t *Table) Default(val interface{}) *Table {
	v := convertToStr(val)
	t.columns[len(t.columns)-1].Default = &v
	return t
}

// Comment sets the column comment
func (t *Table) Comment(cmt string) *Table {
	t.columns[len(t.columns)-1].Comment = &cmt
	return t
}

// TableComment sets the comment for table
func (t *Table) TableComment(cmt string) {
	t.comment = &cmt
}

// Index sets the last column to btree index
func (t *Table) Index(idxName string) *Table {
	t.columns[len(t.columns)-1].IdxName = idxName
	t.columns[len(t.columns)-1].IsIndex = true
	return t
}

// Unique sets the last column to unique index
func (t *Table) Unique(idxName string) *Table {
	t.columns[len(t.columns)-1].IdxName = idxName
	t.columns[len(t.columns)-1].IsUnique = true
	return t
}

// ForeignKey sets the last column to reference rfcTbl on onCol with idxName foreign key index
func (t *Table) ForeignKey(idxName, rfcTbl, onCol string) *Table {
	key := AlterTable + t.tblName + " ADD CONSTRAINT " + idxName + " FOREIGN KEY (" + t.columns[len(t.columns)-1].Name + ") REFERENCES " + rfcTbl + " (" + onCol + ")"
	t.columns[len(t.columns)-1].ForeignKey = &key
	return t
}

func (t *Table) Concurrently() *Table {
	t.columns[len(t.columns)-1].IsIdxConcurrent = true
	return t
}

func (t *Table) Include(columns ...string) *Table {
	t.columns[len(t.columns)-1].Includes = columns
	return t
}

// Date	creates date column with an ability to set current_date as default value
func (t *Table) Date(colNm string, isDefault bool) *Table {
	t.columns = append(t.columns, buildDateTIme(colNm, TypeDate, CurrentDate, isDefault))
	return t
}

// Time creates time column with an ability to set current_time as default value
func (t *Table) Time(colNm string, isDefault bool) *Table {
	t.columns = append(t.columns, buildDateTIme(colNm, TypeTime, CurrentTime, isDefault))
	return t
}

// DateTime creates datetime column with an ability to set NOW() as default value
func (t *Table) DateTime(colNm string, isDefault bool) *Table {
	t.columns = append(t.columns, buildDateTIme(colNm, TypeDateTime, CurrentDateTime, isDefault))
	return t
}

// DateTimeTz creates datetime column with an ability to set NOW() as default value + time zone support
func (t *Table) DateTimeTz(colNm string, isDefault bool) *Table {
	t.columns = append(t.columns, buildDateTIme(colNm, TypeDateTimeTz, CurrentDateTime, isDefault))
	return t
}

// TsVector creates tsvector typed column
func (t *Table) TsVector(colNm string) *Table {
	t.columns = append(t.columns, &column{Name: colNm, ColumnType: TypeTsVector})
	return t
}

// TsQuery creates tsquery typed column
func (t *Table) TsQuery(colNm string) *Table {
	t.columns = append(t.columns, &column{Name: colNm, ColumnType: TypeTsQuery})
	return t
}

// Json creates json text typed column
func (t *Table) Json(colNm string) *Table {
	t.columns = append(t.columns, &column{Name: colNm, ColumnType: TypeJson})
	return t
}

// Jsonb creates jsonb typed column
func (t *Table) Jsonb(colNm string) *Table {
	t.columns = append(t.columns, &column{Name: colNm, ColumnType: TypeJsonb})
	return t
}

// Point creates point geometry typed column
func (t *Table) Point(colNm string) *Table {
	t.columns = append(t.columns, &column{Name: colNm, ColumnType: TypePoint})
	return t
}

// Polygon creates point geometry typed column
func (t *Table) Polygon(colNm string) *Table {
	t.columns = append(t.columns, &column{Name: colNm, ColumnType: TypePolygon})
	return t
}

// build any date/time type with defaults preset
func buildDateTIme(colNm, t, defType string, isDefault bool) *column {
	col := &column{Name: colNm, ColumnType: colType(t)}
	if isDefault {
		col.Default = &defType
	}
	return col
}

// Change the column type/length/nullable etc options
func (t *Table) Change() {
	t.columns[len(t.columns)-1].IsModify = true
}

// IfNotExists add column/index if not exists
func (t *Table) IfNotExists() *Table {
	t.columns[len(t.columns)-1].IfExists = IfNotExists
	return t
}

// IfExists drop column/index if exists
func (t *Table) IfExists() *Table {
	t.columns[len(t.columns)-1].IfExists = IfExists
	return t
}

// Rename the column "from" to the "to"
func (t *Table) Rename(from, to string) *Table {
	t.columns = append(t.columns, &column{Name: from, RenameTo: &to, IsModify: true})
	return t
}

// DropColumn the column named colNm in this table context
func (t *Table) DropColumn(colNm string) *Table {
	t.columns = append(t.columns, &column{Name: colNm, IsDrop: true})
	return t
}

// DropIndex the column named idxNm in this table context
func (t *Table) DropIndex(idxNm string) *Table {
	t.columns = append(t.columns, &column{IdxName: idxNm, IsDrop: true, IsIndex: true})
	return t
}

// createTable create table with relative columns/indices
func (r *DB) createTable(t *Table) (res sql.Result, err error) {
	l := len(t.columns)
	var indices []string
	var comments []string

	query := "CREATE TABLE " + applyExistence(t.ifExists) + t.tblName + "("
	for k, col := range t.columns {
		query += composeColumn(col)
		if k < l-1 {
			query += ","
		}
		indices = append(indices, composeIndex(t.tblName, col))
		comments = append(comments, composeComment(t.tblName, col))
	}
	query += ")"

	res, err = r.Sql().Exec(query)
	if err != nil {
		return nil, err
	}

	// create indices
	_, err = r.createIndices(indices)
	if err != nil {
		return nil, err
	}
	// create comments
	comments = append(comments, t.composeTableComment())
	_, err = r.createComments(comments)
	if err != nil {
		return nil, err
	}
	return
}

// adds, modifies or deletes column
func (r *DB) modifyTable(t *Table) (res sql.Result, err error) {
	l := len(t.columns)

	var indices []string
	var comments []string
	query := ""
	for k, col := range t.columns {
		if col.IsModify {
			col.Op = Modify
			if col.RenameTo != nil {
				col.Op = Rename
			}
			query += composeModifyColumn(t.tblName, col)
		} else if col.IsDrop {
			query += composeDrop(t.tblName, col)
		} else { // create new column/comment/index or just add comments indices
			isCol, _ := r.HasColumns(DefaultSchema, t.tblName, col.Name)
			if !isCol {
				query += composeAddColumn(t.tblName, col)
			}
			indices = append(indices, composeIndex(t.tblName, col))
			comments = append(comments, composeComment(t.tblName, col))
		}

		if k < l-1 {
			query += SemiColon
		}
	}

	res, err = r.Sql().Exec(query)
	if err != nil {
		return nil, err
	}

	// create indices
	_, err = r.createIndices(indices)
	if err != nil {
		return nil, err
	}
	// create comments
	_, err = r.createComments(comments)
	if err != nil {
		return nil, err
	}
	return
}
