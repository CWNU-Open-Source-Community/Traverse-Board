package skills

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// deterministicZipEntry is one fixed-profile ZIP entry. The builder emits the
// exact container profile validatePackageContainer enforces: Deflate with a
// trailing data descriptor, zeroed timestamps, no extra/comment fields.
type deterministicZipEntry struct {
	name string
	data []byte
}

// buildDeterministicPackage serializes entries into a ZIP container with no
// timestamps, comments, extra fields, or other non-deterministic metadata.
// Entries are written in caller order; the container validator requires the
// exact protocol order (manifest.json, SKILL.md, optional SIGNATURE.json), so
// callers must not reorder them.
func buildDeterministicPackage(entries []deterministicZipEntry) ([]byte, error) {
	if len(entries) == 0 {
		return nil, errors.New("package must contain at least one entry")
	}
	var local bytes.Buffer
	var central bytes.Buffer
	type record struct {
		name                     string
		crc                      uint32
		compressed, uncompressed uint32
		offset                   uint32
	}
	records := make([]record, 0, len(entries))
	for _, entry := range entries {
		var deflated bytes.Buffer
		writer, err := flate.NewWriter(&deflated, flate.DefaultCompression)
		if err != nil {
			return nil, err
		}
		if _, err := writer.Write(entry.data); err != nil {
			return nil, err
		}
		if err := writer.Close(); err != nil {
			return nil, err
		}
		record := record{
			name:         entry.name,
			crc:          crc32.ChecksumIEEE(entry.data),
			compressed:   uint32(deflated.Len()),
			uncompressed: uint32(len(entry.data)),
			offset:       uint32(local.Len()),
		}
		// Local file header: version 20, data-descriptor flag, Deflate, zero time.
		if err := binary.Write(&local, binary.LittleEndian, uint32(zipLocalHeaderSignature)); err != nil {
			return nil, err
		}
		if err := binary.Write(&local, binary.LittleEndian, uint16(zipDeflateVersion)); err != nil {
			return nil, err
		}
		if err := binary.Write(&local, binary.LittleEndian, uint16(zipDataDescriptorFlag)); err != nil {
			return nil, err
		}
		if err := binary.Write(&local, binary.LittleEndian, uint16(zip.Deflate)); err != nil {
			return nil, err
		}
		if err := binary.Write(&local, binary.LittleEndian, uint16(0)); err != nil { // time
			return nil, err
		}
		if err := binary.Write(&local, binary.LittleEndian, uint16(0)); err != nil { // date
			return nil, err
		}
		if err := binary.Write(&local, binary.LittleEndian, uint32(0)); err != nil { // crc deferred
			return nil, err
		}
		if err := binary.Write(&local, binary.LittleEndian, uint32(0)); err != nil { // sizes deferred
			return nil, err
		}
		if err := binary.Write(&local, binary.LittleEndian, uint32(0)); err != nil {
			return nil, err
		}
		if err := binary.Write(&local, binary.LittleEndian, uint16(len(entry.name))); err != nil {
			return nil, err
		}
		if err := binary.Write(&local, binary.LittleEndian, uint16(0)); err != nil { // extra
			return nil, err
		}
		if _, err := local.WriteString(entry.name); err != nil {
			return nil, err
		}
		if _, err := local.Write(deflated.Bytes()); err != nil {
			return nil, err
		}
		// Data descriptor.
		if err := binary.Write(&local, binary.LittleEndian, uint32(zipDescriptorSignature)); err != nil {
			return nil, err
		}
		if err := binary.Write(&local, binary.LittleEndian, record.crc); err != nil {
			return nil, err
		}
		if err := binary.Write(&local, binary.LittleEndian, record.compressed); err != nil {
			return nil, err
		}
		if err := binary.Write(&local, binary.LittleEndian, record.uncompressed); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	for _, record := range records {
		// Central directory header.
		if err := binary.Write(&central, binary.LittleEndian, uint32(zipCentralHeaderSignature)); err != nil {
			return nil, err
		}
		if err := binary.Write(&central, binary.LittleEndian, uint16(zipDeflateVersion)); err != nil {
			return nil, err
		}
		if err := binary.Write(&central, binary.LittleEndian, uint16(zipDeflateVersion)); err != nil {
			return nil, err
		}
		if err := binary.Write(&central, binary.LittleEndian, uint16(zipDataDescriptorFlag)); err != nil {
			return nil, err
		}
		if err := binary.Write(&central, binary.LittleEndian, uint16(zip.Deflate)); err != nil {
			return nil, err
		}
		if err := binary.Write(&central, binary.LittleEndian, uint16(0)); err != nil { // time
			return nil, err
		}
		if err := binary.Write(&central, binary.LittleEndian, uint16(0)); err != nil { // date
			return nil, err
		}
		if err := binary.Write(&central, binary.LittleEndian, record.crc); err != nil {
			return nil, err
		}
		if err := binary.Write(&central, binary.LittleEndian, record.compressed); err != nil {
			return nil, err
		}
		if err := binary.Write(&central, binary.LittleEndian, record.uncompressed); err != nil {
			return nil, err
		}
		if err := binary.Write(&central, binary.LittleEndian, uint16(len(record.name))); err != nil {
			return nil, err
		}
		if err := binary.Write(&central, binary.LittleEndian, uint16(0)); err != nil { // extra
			return nil, err
		}
		if err := binary.Write(&central, binary.LittleEndian, uint16(0)); err != nil { // comment
			return nil, err
		}
		if err := binary.Write(&central, binary.LittleEndian, uint16(0)); err != nil { // disk
			return nil, err
		}
		if err := binary.Write(&central, binary.LittleEndian, uint16(0)); err != nil { // internal attrs
			return nil, err
		}
		if err := binary.Write(&central, binary.LittleEndian, uint32(0)); err != nil { // external attrs
			return nil, err
		}
		if err := binary.Write(&central, binary.LittleEndian, record.offset); err != nil {
			return nil, err
		}
		if _, err := central.WriteString(record.name); err != nil {
			return nil, err
		}
	}
	var out bytes.Buffer
	if _, err := out.Write(local.Bytes()); err != nil {
		return nil, err
	}
	centralOffset := out.Len()
	if _, err := out.Write(central.Bytes()); err != nil {
		return nil, err
	}
	// End record: no comment, no ZIP64.
	if err := binary.Write(&out, binary.LittleEndian, uint32(zipEndHeaderSignature)); err != nil {
		return nil, err
	}
	if err := binary.Write(&out, binary.LittleEndian, uint16(0)); err != nil {
		return nil, err
	}
	if err := binary.Write(&out, binary.LittleEndian, uint16(0)); err != nil {
		return nil, err
	}
	if err := binary.Write(&out, binary.LittleEndian, uint16(len(records))); err != nil {
		return nil, err
	}
	if err := binary.Write(&out, binary.LittleEndian, uint16(len(records))); err != nil {
		return nil, err
	}
	if err := binary.Write(&out, binary.LittleEndian, uint32(central.Len())); err != nil {
		return nil, err
	}
	if err := binary.Write(&out, binary.LittleEndian, uint32(centralOffset)); err != nil {
		return nil, err
	}
	if err := binary.Write(&out, binary.LittleEndian, uint16(0)); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
