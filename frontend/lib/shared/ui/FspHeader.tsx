import Image from "next/image";
import Link from "next/link";

export function FspHeader() {
  return (
    <header className="fsp-header">
      <div className="fsp-header-inner">
        <Link href="/" className="fsp-brand" aria-label="Task Per Minute - главная">
          <Image
            src="/brand/mark.webp"
            alt=""
            width={52}
            height={52}
            priority
          />
          <span className="fsp-brand-name">Кубок Федерации<span>2026</span></span>
        </Link>
        <Image
          className="fsp-federation-logo"
          src="/brand/fsp.svg"
          alt="Федерация спортивного программирования России"
          width={280}
          height={35}
        />
        <div className="fsp-event">
          <span>Финал</span><strong>10 октября 2026</strong>
        </div>
      </div>
    </header>
  );
}
