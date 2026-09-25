import { useEffect, useState } from "react";
import { auth } from "./api";
import { useHashRoute } from "./lib/hooks";
import { Empty, Toaster } from "./components";
import Login from "./pages/Login";
import Overview from "./pages/Overview";
import Events from "./pages/Events";
import EventDetail from "./pages/EventDetail";
import DeliveryDetail from "./pages/DeliveryDetail";
import FailedDeliveries from "./pages/FailedDeliveries";
import Endpoints from "./pages/Endpoints";

const NAV: { href: string; label: string; section: string }[] = [
  { href: "#/", label: "Overview", section: "" },
  { href: "#/events", label: "Events", section: "events" },
  { href: "#/failed", label: "Failed deliveries", section: "failed" },
  { href: "#/endpoints", label: "Endpoints", section: "endpoints" },
];

export default function App() {
  const [signedIn, setSignedIn] = useState(() => auth.get() !== null);
  const path = useHashRoute();
  const [, section = "", id] = path.split("/");

  useEffect(() => {
    const onSignedOut = () => setSignedIn(false);
    window.addEventListener("hookrelay:signed-out", onSignedOut);
    return () => window.removeEventListener("hookrelay:signed-out", onSignedOut);
  }, []);

  if (!signedIn) {
    return (
      <>
        <Login onSignedIn={() => setSignedIn(true)} />
        <Toaster />
      </>
    );
  }

  // A delivery belongs under Events in the navigation.
  const activeSection = section === "deliveries" ? "events" : section;

  return (
    <div className="shell">
      <nav className="sidebar" aria-label="Main">
        <a className="brand" href="#/">
          hookrelay
        </a>
        <ul>
          {NAV.map((n) => (
            <li key={n.href}>
              <a href={n.href} aria-current={activeSection === n.section ? "page" : undefined}>
                {n.label}
              </a>
            </li>
          ))}
        </ul>
        <button
          type="button"
          className="signout"
          onClick={() => {
            auth.clear();
            setSignedIn(false);
          }}
        >
          Sign out
        </button>
      </nav>
      <main className="content">{renderRoute(section, id)}</main>
      <Toaster />
    </div>
  );
}

function renderRoute(section: string, id?: string) {
  switch (section) {
    case "":
      return <Overview />;
    case "events":
      return id ? <EventDetail key={id} id={id} /> : <Events />;
    case "deliveries":
      if (id) return <DeliveryDetail key={id} id={id} />;
      break;
    case "failed":
      return <FailedDeliveries />;
    case "endpoints":
      return <Endpoints />;
  }
  return (
    <Empty>
      <p>This page doesn't exist.</p>
      <a href="#/">Go to the overview</a>
    </Empty>
  );
}
