import { useId, useLayoutEffect, useRef, useState } from "react";
import {
  describeRange,
  fromLocalInputValue,
  presetRange,
  TIME_RANGE_PRESETS,
  toLocalInputValue,
  type TimeRange,
  type TimeRangePresetId,
} from "../lib/time";

export interface TimeRangePickerProps {
  value: TimeRange;
  onChange: (range: TimeRange) => void;
  idPrefix?: string;
}

/**
 * Segmented time-range control with a sliding highlight, plus an explicit
 * custom range. The chosen window drives every query of the surrounding page.
 */
export function TimeRangePicker({ value, onChange, idPrefix }: TimeRangePickerProps) {
  const generatedId = useId();
  const prefix = idPrefix ?? generatedId;
  const listRef = useRef<HTMLFieldSetElement>(null);
  const [highlight, setHighlight] = useState({ left: 0, width: 0 });

  const selectPreset = (preset: TimeRangePresetId) => {
    onChange(presetRange(preset));
  };

  const now = new Date();
  const options: Array<{ id: string; label: string }> = [
    ...TIME_RANGE_PRESETS.map((p) => ({ id: p.id, label: p.shortLabel })),
    { id: "custom", label: "Custom" },
  ];
  const activeIndex = Math.max(
    0,
    options.findIndex((o) => o.id === value.preset),
  );

  useLayoutEffect(() => {
    const el = listRef.current?.querySelector<HTMLElement>(".time-range-option.is-selected");
    if (el && listRef.current) {
      const parent = listRef.current.getBoundingClientRect();
      const rect = el.getBoundingClientRect();
      setHighlight({ left: rect.left - parent.left, width: rect.width });
    }
  }, [activeIndex]);

  return (
    <div className="time-range-picker">
      <fieldset className="time-range-presets" ref={listRef}>
        <legend className="sr-only">Time range</legend>
        <span className="seg-highlight" style={{ left: highlight.left, width: highlight.width }} aria-hidden="true" />
        {TIME_RANGE_PRESETS.map((preset) => (
          <label
            key={preset.id}
            className={`time-range-option${value.preset === preset.id ? " is-selected" : ""}`}
            title={preset.label}
          >
            <input
              type="radio"
              name={`${prefix}-range`}
              value={preset.id}
              checked={value.preset === preset.id}
              onChange={() => selectPreset(preset.id)}
            />
            <span>{preset.shortLabel}</span>
          </label>
        ))}
        <label className={`time-range-option${value.preset === "custom" ? " is-selected" : ""}`} title="Custom range">
          <input
            type="radio"
            name={`${prefix}-range`}
            value="custom"
            checked={value.preset === "custom"}
            onChange={() => {
              const from = value.preset === "custom" ? value.from : new Date(now.getTime() - 24 * 60 * 60 * 1000).toISOString();
              const to = value.preset === "custom" ? value.to : now.toISOString();
              onChange({ preset: "custom", from, to });
            }}
          />
          <span>Custom</span>
        </label>
      </fieldset>

      {value.preset === "custom" ? (
        <div className="time-range-custom">
          <label htmlFor={`${prefix}-from`}>From</label>
          <input
            id={`${prefix}-from`}
            type="datetime-local"
            value={toLocalInputValue(new Date(value.from))}
            onChange={(event) => {
              const from = fromLocalInputValue(event.target.value);
              if (from !== "") onChange({ preset: "custom", from, to: value.to });
            }}
          />
          <label htmlFor={`${prefix}-to`}>To</label>
          <input
            id={`${prefix}-to`}
            type="datetime-local"
            value={toLocalInputValue(new Date(value.to))}
            onChange={(event) => {
              const to = fromLocalInputValue(event.target.value);
              if (to !== "") onChange({ preset: "custom", from: value.from, to });
            }}
          />
        </div>
      ) : null}

      <p className="time-range-summary">{describeRange(value)}</p>
    </div>
  );
}
