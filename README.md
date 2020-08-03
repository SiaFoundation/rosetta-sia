rosetta-sia
-----------

This is an implementation of the [Rosetta](https://www.rosetta-api.org)
blockchain standard. Both the Data API and Construction API are supported.

The Data API consists of four services:

- The Block service provides block data, queryable by height or ID. Sia blocks
  must be converted to Rosetta blocks, which requires looking up the value of
  the `SiacoinInputs` in each transaction. Currently, the Sia Rosetta
  implementation does not handle Siafunds at all (although it will properly
  account for siacoins created from a `SiafundClaimOutput`). Support for
  Siafunds may be added in a later release.
- The Account service provides the current balance of any account. (In the Sia
  implementation, an "account" is an address/`UnlockHash`.) It also reports the
  UTXOs controlled by the account, which are called "Coins" in the Rosetta API.
  Importantly, "controlled" does not mean "spendable" -- timelocked UTXOs, such
  as miner rewards, are reported immediately, rather than at their maturity
  height.
- The Mempool service provides a view into the transaction pool, with Sia
  transactions converted to their Rosetta equivalents.
- The Network service reports various metadata, such as active peers, current
  block, and supported operation types.

The Construction API consists of a single service -- the Construction service --
which is by far the most complex. This service allows a client to construct
transactions using the UTXO they control. The client submits a set of "intended
operations" to the API, which turns them into an opaque, unsigned, Sia-encoded
transaction, along with a set of payloads to sign. The client signs the
payloads, and uses another Construction endpoint to add the signatures to the
unsigned transaction. The resulting signed transaction can then be broadcast.
The Construction API is intended to be run in an offline environment, so various
metadata must be piped through the process. In Sia's case, this currently
consists of the public key for each `SiacoinInput`.

The `rosetta-sia` implementation consists of a single type, `RosettaService`,
which implements the interfaces for all of the above services. It subscribes to
updates from Sia's `modules.ConsensusSet` so that it can store service-related
data in its database. Most significantly, it stores the value of all UTXOs, and
associates each address seen in the blockchain with the UTXOs it controls (as of
the most recent block). It also stores the timelocked outputs created by miner
payouts and file contracts. `RosettaService` does not store the blocks
themselves; they are fetched (by ID) from `modules.ConsensusSet`, then converted
to the Rosetta format, and finally augmented with the timelocked outputs.
