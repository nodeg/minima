// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package packet

import (
	"crypto"
	"encoding/binary"
	"fmt"
	"hash"
	"io"
	"strconv"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp/errors"
	"github.com/ProtonMail/go-crypto/openpgp/internal/algorithm"
	"github.com/ProtonMail/go-crypto/openpgp/internal/encoding"
)

// SignatureV3 represents older version 3 signatures. These signatures are less secure
// than version 4 and should not be used to create new signatures. They are included
// here for backwards compatibility to read and validate with older key material.
// See RFC 4880, section 5.2.2.
type SignatureV3 struct {
	SigType      SignatureType
	CreationTime time.Time
	IssuerKeyId  uint64
	PubKeyAlgo   PublicKeyAlgorithm
	Hash         crypto.Hash
	HashTag      [2]byte

	RSASignature     encoding.Field
	DSASigR, DSASigS encoding.Field
}

func (sig *SignatureV3) parse(r io.Reader) (err error) {
	// RFC 4880, section 5.2.2
	var buf [8]byte
	if _, err = readFull(r, buf[:1]); err != nil {
		return
	}
	if buf[0] < 2 || buf[0] > 3 {
		err = errors.UnsupportedError("signature packet version " + strconv.Itoa(int(buf[0])))
		return
	}
	if _, err = readFull(r, buf[:1]); err != nil {
		return
	}
	if buf[0] != 5 {
		err = errors.UnsupportedError(
			"invalid hashed material length " + strconv.Itoa(int(buf[0])))
		return
	}

	// Read hashed material: signature type + creation time
	if _, err = readFull(r, buf[:5]); err != nil {
		return
	}
	sig.SigType = SignatureType(buf[0])
	t := binary.BigEndian.Uint32(buf[1:5])
	sig.CreationTime = time.Unix(int64(t), 0)

	// Eight-octet Key ID of signer.
	if _, err = readFull(r, buf[:8]); err != nil {
		return
	}
	sig.IssuerKeyId = binary.BigEndian.Uint64(buf[:])

	// Public-key and hash algorithm
	if _, err = readFull(r, buf[:2]); err != nil {
		return
	}
	sig.PubKeyAlgo = PublicKeyAlgorithm(buf[0])
	switch sig.PubKeyAlgo {
	case PubKeyAlgoRSA, PubKeyAlgoRSASignOnly, PubKeyAlgoDSA:
	default:
		err = errors.UnsupportedError("public key algorithm " + strconv.Itoa(int(sig.PubKeyAlgo)))
		return
	}
	var ok bool
	if sig.Hash, ok = algorithm.HashIdToHashWithSha1Md5(buf[1]); !ok {
		return errors.UnsupportedError("hash function " + strconv.Itoa(int(buf[1])))
	}

	// Two-octet field holding left 16 bits of signed hash value.
	if _, err = readFull(r, sig.HashTag[:2]); err != nil {
		return
	}

	switch sig.PubKeyAlgo {
	case PubKeyAlgoRSA, PubKeyAlgoRSASignOnly:
		sig.RSASignature = new(encoding.MPI)
		_, err = sig.RSASignature.ReadFrom(r)
	case PubKeyAlgoDSA:
		sig.DSASigR = new(encoding.MPI)
		if _, err = sig.DSASigR.ReadFrom(r); err != nil {
			return
		}

		sig.DSASigS = new(encoding.MPI)
		_, err = sig.DSASigS.ReadFrom(r)
	default:
		panic("unreachable")
	}
	return
}

// Serialize marshals sig to w. Sign, SignUserId or SignKey must have been
// called first.
func (sig *SignatureV3) Serialize(w io.Writer) (err error) {
	buf := make([]byte, 8)

	// Write the sig type and creation time
	buf[0] = byte(sig.SigType)
	binary.BigEndian.PutUint32(buf[1:5], uint32(sig.CreationTime.Unix()))
	if _, err = w.Write(buf[:5]); err != nil {
		return
	}

	// Write the issuer long key ID
	binary.BigEndian.PutUint64(buf[:8], sig.IssuerKeyId)
	if _, err = w.Write(buf[:8]); err != nil {
		return
	}

	// Write public key algorithm, hash ID, and hash value
	buf[0] = byte(sig.PubKeyAlgo)
	hashId, ok := algorithm.HashToHashIdWithSha1Md5(sig.Hash)
	if !ok {
		return errors.UnsupportedError(fmt.Sprintf("hash function %v", sig.Hash))
	}
	buf[1] = hashId
	copy(buf[2:4], sig.HashTag[:])
	if _, err = w.Write(buf[:4]); err != nil {
		return
	}

	if sig.RSASignature.Bytes() == nil && sig.DSASigR.Bytes() == nil {
		return errors.InvalidArgumentError("Signature: need to call Sign, SignUserId or SignKey before Serialize")
	}

	switch sig.PubKeyAlgo {
	case PubKeyAlgoRSA, PubKeyAlgoRSASignOnly:
		_, err = w.Write(sig.RSASignature.EncodedBytes())
	case PubKeyAlgoDSA:
		if _, err = w.Write(sig.DSASigR.EncodedBytes()); err != nil {
			return
		}
		_, err = w.Write(sig.DSASigS.EncodedBytes())
	default:
		panic("impossible")
	}
	return
}

// PrepareVerify returns an empty hash object.
func (sig *SignatureV3) PrepareVerify() (hash.Hash, error) {
	if !sig.Hash.Available() {
		return nil, errors.UnsupportedError("hash function")
	}
	return sig.Hash.New(), nil
}
