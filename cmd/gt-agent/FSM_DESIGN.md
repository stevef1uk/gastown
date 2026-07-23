# FSM-Based LLM Response Parser - Design Document

## Problem

The current preprocessOrchestratedResponse() pipeline applies 20+ sequential string
transformations. This is fragile (string-indexing breaks on format variations),
hard to extend, and redundant (each function scans full text independently).

## Solution: cassuservice-Inspired Two-Layer FSM

Layer 1: Outer FSM scans response character-by-character detecting format openings.
Layer 2: Sub-parsers extract tool calls from each format family.

## Format Families and Sub-Parsers

  DSML       -> parseDSML()        handles <DSML> and DeepSeek v4 full-width bar
  Function   -> parseFunction()    handles <Function> XML blocks
  CMD Tags   -> parseCMDTag()      handles <CMD: cmd> and <CMD>block</CMD>
  JSON       -> parseJSON()        handles all 4 JSON format variants
  Fences     -> parseFence()       handles markdown ``` fences
  Channel    -> inline scan        handles <|...|> markers
  Native     -> passthrough        CMD:/READ:/EDIT:/WRITE: lines

## Outer FSM States

  TEXT, JSON, DSML, FUNCTION, CMD_TAG, MD_FENCE, BACKTICK, CHANNEL

## DSML Sub-Parser States

  DSML_TEXT, DSML_INVOKE, DSML_PARAM_TAG, DSML_PARAM_CONTENT, DSML_PARAM_CLOSE

## Function Sub-Parser States

  FUNC_TEXT, FUNC_NAME, FUNC_ARGS

## Implementation Phases

Phase 1: DSML Sub-Parser (fixes DeepSeek v4)
Phase 2: Function + CMD Tag Sub-Parsers
Phase 3: JSON Sub-Parser (consolidate 4 handlers)
Phase 4: Markdown Fence Sub-Parser
Phase 5: Outer FSM Integration
Phase 6: Cleanup (remove old functions, add benchmarks)

## Functions Replaced

splitDSMLBlocks, extractDSMLCommand -> parseDSML()
splitFunctionBlocks, extractFunctionCommand -> parseFunction()
unwrapAngleBracketCMD -> parseCMDTag()
unwrapJSONOrchestratedCommands, unwrapJSONActionCommands,
  unwrapJSONToolCallCommand, unwrapJSONCommandArray -> parseJSON()
unwrapMarkdownFencedToolBlocks -> parseFence()

## Functions Kept (Simple Transforms)

stripChannelMarkers, normalizeGluedWriteBody, normalizeNativeEditEndLines,
stripMarkdownFenceOnlyLines, stripOrchestratedShellBackticks,
normalizeGluedCMDMarkers, glued separator regexes

## Migration: Implement behind existing pipeline, test, then swap.

---

## DeepSeek v4 DSML Format (ACTUAL - from production logs)

### Format A: Standard DSML (older models)

Single tool call, flat structure:
```xml
<DSML><invoke name="bash"><parameter name="command" string="true">export BEADS_DIR=x && bd list --limit=0
