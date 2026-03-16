# Patch: Store GPT full response in mempool variable

## File

`src/plugins/lua/gpt.lua`

## What

After GPT classifies a message, store the full result (probability, reason, categories, model) as a JSON string in a task mempool variable `gpt_result`. This allows downstream postfilter plugins to access the GPT verdict without coupling to the GPT module.

## Where

In `insert_results()` function. Insert the new block **after** the `reason_header` block (line ~778, after the `end` that closes `if result.reason and settings.reason_header`) and **before** `local cache_key = redis_cache_key(sel_part)`.

## Patch

```lua
  -- After this existing block:
  --   if result.reason and settings.reason_header then
  --     ...
  --   end
  -- END OF EXISTING BLOCK

  -- Store full GPT result in mempool for downstream plugins (e.g. user profiling)
  local gpt_mempool = {
    probability = result.probability,
    reason = result.reason or '',
    categories = result.categories or {},
  }
  -- Add model name from consensus results if available
  if result.model then
    gpt_mempool.model = result.model
  end
  local ok_mp, gpt_mp_json = pcall(ucl.to_format, gpt_mempool, 'json-compact')
  if ok_mp then
    task:get_mempool():set_variable('gpt_result', gpt_mp_json)
  end

  -- EXISTING CODE CONTINUES:
  -- local cache_key = redis_cache_key(sel_part)
```

## Also

In `check_consensus_and_insert_results()`, the individual model results have a `model` field. Pass it through to `insert_results` by adding it to the consensus result. Find where `final_result` is built (around line 830-850) and ensure `final_result.model` is set:

```lua
-- When building final_result from consensus, add model info:
final_result.model = table.concat(
  fun.totable(fun.map(function(r) return r.model or 'unknown' end, results)),
  ', ')
```

## Consumer

Any postfilter plugin can read the verdict:

```lua
local gpt_json = task:get_mempool():get_variable('gpt_result')
-- Returns JSON string like: {"probability":0.12,"reason":"...","categories":[],"model":"gpt-5.4"}
```

## Patch 2: Make consensus thresholds configurable

In `check_consensus_and_insert_results()` (line ~850), the hardcoded thresholds 0.75 and 0.25 prevent single-model setups from reaching consensus.

Replace:
```lua
  if nspam > nham and max_spam_prob > 0.75 then
```
with:
```lua
  if nspam > nham and max_spam_prob > (settings.consensus_spam_threshold or 0.75) then
```

And replace:
```lua
  elseif nham > nspam and max_ham_prob < 0.25 then
```
with:
```lua
  elseif nham > nspam and max_ham_prob < (settings.consensus_ham_threshold or 0.25) then
```

Add to `default_options`:
```lua
  consensus_spam_threshold = 0.75,
  consensus_ham_threshold = 0.25,
```

This allows users to configure:
```ucl
# local.d/gpt.conf
consensus_spam_threshold = 0.6;  # lower for single-model setups
consensus_ham_threshold = 0.4;
```

## Notes

- `ucl` is already imported at the top of gpt.lua
- `task:get_mempool():set_variable(name, value)` accepts string values only
- The variable is scoped to the current task (one message scan lifecycle)
- `pcall` wraps the JSON serialization to avoid crashes on edge cases
