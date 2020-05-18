package types

import (
	"errors"
	"net/url"

	"github.com/tendermint/tendermint/crypto/merkle"

	ics23 "github.com/confio/ics23/go"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/cosmos/cosmos-sdk/x/ibc/23-commitment/exported"
	host "github.com/cosmos/cosmos-sdk/x/ibc/24-host"
)

// ICS 023 Merkle Types Implementation
//
// This file defines Merkle commitment types that implements ICS 023.

// Merkle proof implementation of the Proof interface
// Applied on SDK-based IBC implementation
var _ exported.Root = MerkleRoot{}

// MerkleRoot defines a merkle root hash.
// In the Cosmos SDK, the AppHash of a block header becomes the root.
type MerkleRoot struct {
	Hash []byte `json:"hash" yaml:"hash"`
}

// NewMerkleRoot constructs a new MerkleRoot
func NewMerkleRoot(hash []byte) MerkleRoot {
	return MerkleRoot{
		Hash: hash,
	}
}

// GetCommitmentType implements RootI interface
func (MerkleRoot) GetCommitmentType() exported.Type {
	return exported.Merkle
}

// GetHash implements RootI interface
func (mr MerkleRoot) GetHash() []byte {
	return mr.Hash
}

// IsEmpty returns true if the root is empty
func (mr MerkleRoot) IsEmpty() bool {
	return len(mr.GetHash()) == 0
}

var _ exported.Prefix = MerklePrefix{}

// MerklePrefix is merkle path prefixed to the key.
// The constructed key from the Path and the key will be append(Path.KeyPath, append(Path.KeyPrefix, key...))
type MerklePrefix struct {
	KeyPrefix []byte `json:"key_prefix" yaml:"key_prefix"` // byte slice prefixed before the key
}

// NewMerklePrefix constructs new MerklePrefix instance
func NewMerklePrefix(keyPrefix []byte) MerklePrefix {
	return MerklePrefix{
		KeyPrefix: keyPrefix,
	}
}

// GetCommitmentType implements Prefix interface
func (MerklePrefix) GetCommitmentType() exported.Type {
	return exported.Merkle
}

// Bytes returns the key prefix bytes
func (mp MerklePrefix) Bytes() []byte {
	return mp.KeyPrefix
}

// IsEmpty returns true if the prefix is empty
func (mp MerklePrefix) IsEmpty() bool {
	return len(mp.Bytes()) == 0
}

var _ exported.Path = MerklePath{}

// MerklePath is the path used to verify commitment proofs, which can be an arbitrary
// structured object (defined by a commitment type).
type MerklePath struct {
	KeyPaths []merkle.KeyPath `json:"key_paths" yaml:"key_paths"` // byte slice prefixed before the key
}

// NewMerklePath creates a new MerklePath instance
func NewMerklePath(keyPathStr []string) MerklePath {
	merkleKeyPath := merkle.KeyPath{}
	for _, keyStr := range keyPathStr {
		merkleKeyPath = merkleKeyPath.AppendKey([]byte(keyStr), merkle.KeyEncodingURL)
	}

	return MerklePath{
		KeyPaths: []merkle.KeyPath{merkleKeyPath},
	}
}

// GetCommitmentType implements PathI
func (MerklePath) GetCommitmentType() exported.Type {
	return exported.Merkle
}

// String implements fmt.Stringer.
func (mp MerklePath) String() string {
	if len(mp.KeyPaths) == 0 {
		return ""
	}
	pathStr := mp.KeyPaths[0].String()
	for i, p := range mp.KeyPaths {
		if i != 0 {
			pathStr += "/" + p.String()
		}
	}
	return pathStr
}

// Pretty returns the unescaped path of the URL string.
func (mp MerklePath) Pretty() string {
	path, err := url.PathUnescape(mp.String())
	if err != nil {
		panic(err)
	}
	return path
}

// IsEmpty returns true if the path is empty
func (mp MerklePath) IsEmpty() bool {
	return len(mp.KeyPaths) == 0
}

// ApplyPrefix constructs a new commitment path from the arguments. It interprets
// the path argument in the context of the prefix argument.
//
// ApplyPrefix will prepend a new Merkle KeyPath to the existing KeyPaths. This
// allows a single path to be represent a chained merkle proof
//
// CONTRACT: provided path string MUST be a well formated path. See ICS24 for
// reference.
func ApplyPrefix(prefix exported.Prefix, path exported.Path) (exported.Path, error) {
	err := host.DefaultPathValidator(path.String())
	if err != nil {
		return MerklePath{}, err
	}

	if prefix == nil || prefix.IsEmpty() {
		return MerklePath{}, errors.New("prefix can't be empty")
	}
	mpath, ok := path.(MerklePath)
	if !ok {
		return MerklePath{}, errors.New("path is not a merklepath")
	}
	// Turn prefix into a merklepath
	prefixPath := NewMerklePath([]string{string(prefix.Bytes())})
	kpath := append(prefixPath.KeyPaths, mpath.KeyPaths...)
	// Return MerklePath with prefix path prepended to list of paths
	return MerklePath{
		KeyPaths: kpath,
	}, nil
}

var _ exported.Proof = MerkleProof{}

// MerkleProof is a wrapper type that contains list of merkle proofs that can
// be chained to prove membership or non-membership of an element up to the final root.
// The list is ordered from lowest subtree to the final tree. Each proof's root is used
// as the proving value for the subsequent proof
// It demonstrates membership or non-membership for an element or set of elements,
// verifiable in conjunction with a known commitment root. Proofs should be
// succinct.
type MerkleProof struct {
	Proofs []*ics23.CommitmentProof `json:"proofs" yaml:"proofs"`
	Specs  []*ics23.ProofSpec       `json:"specs" yaml:"specs"`
}

// GetCommitmentType implements ProofI
func (MerkleProof) GetCommitmentType() exported.Type {
	return exported.Merkle
}

// VerifyMembership verifies the membership pf a merkle proof against the given root, path, and value.
func (proof MerkleProof) VerifyMembership(root exported.Root, path exported.Path, value []byte) error {
	if proof.ValidateBasic() != nil || root == nil || root.IsEmpty() || path == nil || path.IsEmpty() || len(value) == 0 {
		return sdkerrors.Wrap(ErrInvalidProof, "empty params or proof")
	}

	mpath, ok := path.(MerklePath)
	if !ok {
		return sdkerrors.Wrap(ErrInvalidProof, "path is not a merkle path for a merkle proof")
	}

	if len(proof.Proofs) == len(mpath.KeyPaths) {
		return sdkerrors.Wrapf(ErrInvalidProof, "invalid chained proof. proof chain length %d not the same as path length %d", len(proof.Proofs), len(mpath.KeyPaths))
	}

	for i, p := range proof.Proofs {
		// While the proofs go from lowest subtree to the final tree, the path is defined from the root
		// down to the leaf. Thus, we must pass in subpaths in reverse order during chained proof verification
		subpath := []byte(mpath.KeyPaths[len(mpath.KeyPaths)-i-1].String())
		existProof, ok := p.Proof.(*ics23.CommitmentProof_Exist)
		if !ok {
			return sdkerrors.Wrap(ErrInvalidProof, "proof is not an existence proof")
		}
		// For subtree verification, we simply calculate the root from the proof and use it to prove
		// against the value
		subroot, err := existProof.Exist.Calculate()
		if err != nil {
			return sdkerrors.Wrap(ErrInvalidProof, err.Error())
		}
		if i != len(proof.Proofs)-1 {
			if !ics23.VerifyMembership(proof.Specs[i], subroot, p, subpath, value) {
				return sdkerrors.Wrapf(ErrInvalidProof, "invalid proof for path: %s", path.String())
			}
		} else {
			// For the final verification, we prove inclusion against the root that was passed into function
			// rather than calculating subroot in order to verify that the given value was committed to by
			// the given root
			if !ics23.VerifyMembership(proof.Specs[i], root.GetHash(), p, subpath, value) {
				return sdkerrors.Wrapf(ErrInvalidProof, "invalid proof for path: %s", path.String())
			}
		}
		// Each subtree root becomes the proving value for the next proof in the chaining process
		value = subroot
	}
	return nil
}

// VerifyNonMembership verifies the absence of a merkle proof against the given root and path.
// VerifyNonMembership verifies a chained proof where the absence of a given path is proven
// at the lowest subtree and then each subtree's inclusion is proved up to the final root.
func (proof MerkleProof) VerifyNonMembership(root exported.Root, path exported.Path) error {
	if proof.ValidateBasic() != nil || root == nil || root.IsEmpty() || path == nil || path.IsEmpty() {
		return sdkerrors.Wrap(ErrInvalidProof, "empty params or proof")
	}

	mpath, ok := path.(MerklePath)
	if !ok {
		return sdkerrors.Wrap(ErrInvalidProof, "path is not a merkle path for a merkle proof")
	}

	if len(proof.Proofs) == len(mpath.KeyPaths) {
		return sdkerrors.Wrapf(ErrInvalidProof, "invalid chained proof. proof chain length %d not the same as path length %d", len(proof.Proofs), len(mpath.KeyPaths))
	}

	var value, subroot []byte
	var err error
	for i, p := range proof.Proofs {
		// While the proofs go from lowest subtree to the final tree, the path is defined from the root
		// down to the leaf. Thus, we must pass in subpaths in reverse order during chained proof verification
		subpath := []byte(mpath.KeyPaths[len(mpath.KeyPaths)-i-1].String())
		if i == 0 {
			// The first proof, thus the proof for the lowest subtree, is a nonexistence proof.
			// Thus, we calculate the root from proof and then prove nonexistence of the path against this root
			nonexistProof, ok := p.Proof.(*ics23.CommitmentProof_Nonexist)
			if !ok {
				return sdkerrors.Wrap(ErrInvalidProof, "proof is not a nonexistence proof")
			}
			subroot, err = nonexistProof.Nonexist.Left.Calculate()
			if err != nil {
				return sdkerrors.Wrap(ErrInvalidProof, err.Error())
			}

			if !ics23.VerifyNonMembership(proof.Specs[i], subroot, p, subpath) {
				return sdkerrors.Wrapf(ErrInvalidProof, "invalid proof for path: %s", path.String())
			}
		} else {
			// Each subsequent proof is a proof of inclusion of the **previous** subtree's root
			if i != len(proof.Proofs)-1 {
				// For intermediate proofs, we calculate the subroot from the proof and prove the previous subtree's
				// root in this higher subroot
				existProof, ok := p.Proof.(*ics23.CommitmentProof_Exist)
				if !ok {
					return sdkerrors.Wrap(ErrInvalidProof, "proof is not an existence proof")
				}
				subroot, err = existProof.Exist.Calculate()
				if err != nil {
					return sdkerrors.Wrap(ErrInvalidProof, err.Error())
				}

				if !ics23.VerifyMembership(proof.Specs[i], subroot, p, subpath, value) {
					return sdkerrors.Wrapf(ErrInvalidProof, "invalid proof for path: %s", path.String())
				}
			} else {
				// The final proof in the chain will prove inclusion against the given root.
				if !ics23.VerifyMembership(proof.Specs[i], root.GetHash(), p, subpath, value) {
					return sdkerrors.Wrapf(ErrInvalidProof, "invalid proof for path: %s", path.String())
				}
			}
		}
		// Each subtree root becomes the proving value for the next proof in the chaining process
		value = subroot
	}

	return nil

}

// IsEmpty returns true if MerkleProof is empty
func (proof MerkleProof) IsEmpty() bool {
	if len(proof.Proofs) == 0 || len(proof.Specs) == 0 {
		return true
	}
	for i, p := range proof.Proofs {

		if p == nil || proof.Specs[i] == nil {
			return true
		}
	}

	return false
}

// ValidateBasic checks if the proof is empty or malformed.
func (proof MerkleProof) ValidateBasic() error {
	if proof.IsEmpty() || len(proof.Proofs) != len(proof.Specs) {
		return ErrInvalidProof
	}
	return nil
}
