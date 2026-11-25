# SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

def filled: type == "string" and test("[^[:space:]]");
def statuses: ["not_affected", "affected", "fixed", "under_investigation"];
def justifications: [
  "component_not_present",
  "vulnerable_code_not_present",
  "vulnerable_code_not_in_execute_path",
  "vulnerable_code_cannot_be_controlled_by_adversary",
  "inline_mitigations_already_exist"
];
def require($condition; $message):
  if $condition then . else error($message) end;

require(type == "object"; "document must be an object")
| require(."@context" == "https://openvex.dev/ns/v0.2.0"; "unsupported OpenVEX context")
| require(."@id" | filled; "document @id is required")
| require(.author | filled; "author is required")
| require(.timestamp | filled; "timestamp is required")
| require(.timestamp | test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\\.[0-9]+)?Z$"); "timestamp must be UTC RFC 3339")
| require(.version | type == "number"; "version must be a number")
| require(.version >= 1 and (.version | floor) == .version; "version must be a positive integer")
| require(.statements | type == "array"; "statements must be an array")
| require(.statements | length > 0; "statements must not be empty")
| .statements[]
| require(type == "object"; "statement must be an object")
| require(.vulnerability.name | filled; "vulnerability.name is required")
| require(.status | type == "string"; "status must be a string")
| require(.status as $status | statuses | index($status); "unsupported status")
| require(.products | type == "array"; "products must be an array")
| require(.products | length > 0; "products must not be empty")
| require(all(.products[];
    (.identifiers.purl | type == "string")
    and (.identifiers.purl | test("^pkg:oci/controller@sha256:[a-f0-9]{64}$"))
    and ."@id" == .identifiers.purl
  ); "products must use matching @id and purl bound to controller platform digests")
| require(.justification == null or (.justification | type == "string"); "justification must be a string")
| require(.justification == null or (.justification as $j | justifications | index($j)); "unsupported justification")
| require(.status != "not_affected" or (.impact_statement | filled); "not_affected requires reviewed impact evidence")
| require(.status != "affected" or (.action_statement | filled); "affected requires an action_statement")
| true
