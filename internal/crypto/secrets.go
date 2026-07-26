// SPDX-License-Identifier: AGPL-3.0-or-later

package crypto

// TransformSlots applies op to every non-empty string slot, in place.
//
// It is the one mechanism every secret-bearing persistence path shares.
// Callers differ only in how they enumerate slots: typed settings rows
// hand over pointers to their secret fields; metadata provider config
// discovers its password keys from the provider's runtime schema.
//
// Empty slots and nil pointers are skipped — an unset secret stays
// unset rather than becoming a ciphertext of "". Transformation is
// all-or-nothing: if op fails for any slot, no slot is modified, so a
// half-encrypted config never reaches the database.
func TransformSlots(op func(string) (string, error), slots []*string) error {
	staged := make(map[int]string, len(slots))
	for i, slot := range slots {
		if slot == nil || *slot == "" {
			continue
		}
		out, err := op(*slot)
		if err != nil {
			return err
		}
		staged[i] = out
	}
	for i, v := range staged {
		*slots[i] = v
	}
	return nil
}
