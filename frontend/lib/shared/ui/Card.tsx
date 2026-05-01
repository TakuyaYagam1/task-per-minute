import React from "react";

interface CardProps {
  children: React.ReactNode;
  className?: string;
  style?: React.CSSProperties;
}

const CARD_STYLES = {
  backgroundColor: "rgba(112, 206, 206, 0.8)",
  borderRadius: "1rem",
  padding: "1.5rem",
  boxShadow: "0 8px 32px rgba(0, 0, 0, 0.6)",
  color: "#FFF",
};

export const Card: React.FC<CardProps> = ({ children, className, style }) => {
  return (
    <div className={className} style={{ ...CARD_STYLES, ...style }}>
      {children}
    </div>
  );
};
