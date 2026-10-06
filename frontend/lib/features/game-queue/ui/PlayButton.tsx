"use client";

import React from "react";

interface PlayButtonProps {
  onClick: () => void;
  disabled?: boolean;
  ref?: React.Ref<HTMLButtonElement>;
}

export const PlayButton: React.FC<PlayButtonProps> = ({
  onClick,
  disabled = false,
  ref,
}) => {
  return (
    <button
      ref={ref}
      type="button"
      onClick={onClick}
      disabled={disabled}
      className="btn btn-primary"
      style={{ width: "100%", minHeight: "3.25rem", fontWeight: 600 }}
    >
      {disabled ? "ЗАГРУЗКА..." : "ИГРАТЬ"}
      {!disabled && <span aria-hidden="true">-&gt;</span>}
    </button>
  );
};
