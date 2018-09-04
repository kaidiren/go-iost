package rocksdb

/*
#include "crocksdb.h"
#include <stdlib.h>
#include <unistd.h>
#cgo CFLAGS: -I${SRCDIR}/include
#cgo darwin LDFLAGS: -L${SRCDIR}/lib -lrocksdb -lstdc++ -lz -lbz2 -Wl,-rpath,${SRCDIR}/lib
#cgo linux LDFLAGS: -L${SRCDIR}/lib -lrocksdb -lstdc++ -lz -lbz2 -Wl,-rpath=${SRCDIR}/lib
*/
import "C"
import (
	"fmt"
	"unsafe"
)

type DB struct {
	cdb       *C.rocksdb_t
	cbatch    *C.rocksdb_writebatch_t
	croptions *C.rocksdb_readoptions_t
	cwoptions *C.rocksdb_writeoptions_t
}

func NewDB(path string) (*DB, error) {
	var options *C.rocksdb_options_t = C.rocksdb_options_create()
	defer C.rocksdb_options_destroy(options)

	var cpus C.long = C.sysconf(C._SC_NPROCESSORS_ONLN)
	C.rocksdb_options_increase_parallelism(options, C.int(cpus))
	C.rocksdb_options_optimize_level_style_compaction(options, 0)
	C.rocksdb_options_set_create_if_missing(options, 1)

	var cerr *C.char
	defer C.free(unsafe.Pointer(cerr))

	var db *C.rocksdb_t = C.rocksdb_open(options, C.CString(path), &cerr)
	var croptions *C.rocksdb_readoptions_t = C.rocksdb_readoptions_create()
	var cwoptions *C.rocksdb_writeoptions_t = C.rocksdb_writeoptions_create()

	err := C.GoString(cerr)

	if err != "" {
		return nil, fmt.Errorf("failed to open rocksdb: %v", err)
	}

	return &DB{
		cdb:       db,
		cbatch:    nil,
		croptions: croptions,
		cwoptions: cwoptions,
	}, nil
}

func (d *DB) Get(key []byte) ([]byte, error) {
	var ckey *C.char = C.CString(string(key))
	defer C.free(unsafe.Pointer(ckey))
	var ckeylen C.size_t = C.size_t(len(key))

	var cerr *C.char
	defer C.free(unsafe.Pointer(cerr))

	var clen C.size_t
	var cvalue *C.char = C.rocksdb_get(d.cdb, d.croptions, ckey, ckeylen, &clen, &cerr)
	defer C.free(unsafe.Pointer(cvalue))

	err := C.GoString(cerr)
	value := C.GoString(cvalue)

	if err != "" {
		return nil, fmt.Errorf("failed to get by rocksdb: %v", err)
	}
	return []byte(value), nil
}

func (d *DB) Put(key []byte, value []byte) error {
	var ckey *C.char = C.CString(string(key))
	defer C.free(unsafe.Pointer(ckey))
	var ckeylen C.size_t = C.size_t(len(key))

	var cvalue *C.char = C.CString(string(value))
	defer C.free(unsafe.Pointer(cvalue))
	var cvaluelen C.size_t = C.size_t(len(value))

	var cerr *C.char
	defer C.free(unsafe.Pointer(cerr))

	C.rocksdb_put(d.cdb, d.cwoptions, ckey, ckeylen, cvalue, cvaluelen+1, &cerr)

	err := C.GoString(cerr)

	if err != "" {
		return fmt.Errorf("failed to put by rocksdb: %v", err)
	}
	return nil
}

func (d *DB) Has(key []byte) (bool, error) {
	var ckey *C.char = C.CString(string(key))
	defer C.free(unsafe.Pointer(ckey))
	var ckeylen C.size_t = C.size_t(len(key))

	var cerr *C.char
	defer C.free(unsafe.Pointer(cerr))

	var clen C.size_t
	var cvalue *C.char = C.rocksdb_get(d.cdb, d.croptions, ckey, ckeylen, &clen, &cerr)
	defer C.free(unsafe.Pointer(cvalue))

	err := C.GoString(cerr)
	value := C.GoString(cvalue)

	if err != "" {
		return false, fmt.Errorf("failed to has by rocksdb: %v", err)
	}
	return value != "", nil
}

func (d *DB) Delete(key []byte) error {
	var ckey *C.char = C.CString(string(key))
	defer C.free(unsafe.Pointer(ckey))
	var ckeylen C.size_t = C.size_t(len(key))

	var cerr *C.char
	defer C.free(unsafe.Pointer(cerr))

	C.rocksdb_delete(d.cdb, d.cwoptions, ckey, ckeylen, &cerr)

	err := C.GoString(cerr)

	if err != "" {
		return fmt.Errorf("failed to delete by rocksdb: %v", err)
	}
	return nil
}

func (d *DB) Keys(prefix []byte) ([][]byte, error) {
	var croptions *C.rocksdb_readoptions_t = C.rocksdb_readoptions_create()
	defer C.rocksdb_readoptions_destroy(croptions)

	var ckey *C.char = C.CString(string(prefix))
	defer C.free(unsafe.Pointer(ckey))
	var ckeylen C.size_t = C.size_t(len(prefix))

	C.rocksdb_readoptions_set_iterate_upper_bound(croptions, ckey, ckeylen)
	C.rocksdb_readoptions_set_iterate_lower_bound(croptions, ckey, ckeylen)

	var iter *C.rocksdb_iterator_t = C.rocksdb_create_iterator(d.cdb, croptions)
	defer C.rocksdb_iter_destroy(iter)

	keys := make([][]byte, 0)
	for C.rocksdb_iter_seek_to_first(iter); C.rocksdb_iter_valid(iter) != 0; C.rocksdb_iter_next(iter) {
		var ckeylen C.size_t
		var ckey *C.char = C.rocksdb_iter_key(iter, &ckeylen)
		defer C.free(unsafe.Pointer(ckey))

		key := C.GoString(ckey)

		keys = append(keys, []byte(key))
	}

	var cerr *C.char
	defer C.free(unsafe.Pointer(cerr))
	C.rocksdb_iter_get_error(iter, &cerr)

	err := C.GoString(cerr)

	if err != "" {
		return nil, fmt.Errorf(err)
	}

	return keys, nil
}

func (d *DB) BeginBatch() error {
	if d.cbatch != nil {
		return fmt.Errorf("not support nested batch write")
	}

	var cbatch *C.rocksdb_writebatch_t = C.rocksdb_writebatch_create()
	d.cbatch = cbatch
	return nil
}

func (d *DB) CommitBatch() error {
	if d.cbatch == nil {
		return fmt.Errorf("no batch write to commit")
	}

	var cerr *C.char
	defer C.free(unsafe.Pointer(cerr))

	C.rocksdb_write(d.cdb, d.cwoptions, d.cbatch, &cerr)

	err := C.GoString(cerr)

	if err != "" {
		return fmt.Errorf("failed to write batch: %v", err)
	}

	C.rocksdb_writebatch_destroy(d.cbatch)
	d.cbatch = nil
	return nil
}

func (d *DB) Close() error {
	C.rocksdb_close(d.cdb)
	C.rocksdb_writebatch_destroy(d.cbatch)
	C.rocksdb_readoptions_destroy(d.croptions)
	C.rocksdb_writeoptions_destroy(d.cwoptions)

	return nil
}