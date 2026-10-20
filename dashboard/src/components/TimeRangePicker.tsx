import { useId } from "react";
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
 * Time window picker: relative presets plus an explicit custom range.
 *
 * The chosen window drives every query of the surrounding page.
 */
export function TimeRangePicker({ value, onChange, idPrefix }: TimeRangePickerProps) {
  const generatedId = useId();
  const prefix = idPrefix ?? generatedId;

  const selectPreset = (preset: TimeRangePresetId) => {
    onChange(presetRange(preset));
  };

  const now = new Date();

  return (
    <div className="time-range-picker">
      <fieldset className="time-range-presets">
        <legend className="sr-only">Time range</legend>
        {TIME_RANGE_PRESETS.map((preset) => (
          <label
            key={preset.id}
            className={`time-range-option${value.preset === preset.id ? " is-selected" : ""}`}
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
        <label
          className={`time-range-option${value.preset === "custom" ? " is-selected" : ""}`}
        >
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
