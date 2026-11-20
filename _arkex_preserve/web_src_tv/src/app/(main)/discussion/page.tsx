"use client";

import { useEffect, useState, type FormEvent } from "react";
import { useWorkspace } from "@/components/workspace";

export default function DiscussionPage() {
  const { engagements, selected } = useWorkspace();
  const engagement = engagements.find(({ id }) => id === selected);

  return (
    <div className="page-container py-8">
      <div className="workspace-heading"><div><h1>Team Discussion</h1><p>Draft engagement notes. Drafts stay in this browser.</p></div></div>
      {engagement ? (
        <DiscussionDraft key={engagement.id} storageKey={`arkex-discussion-${engagement.id}`} title={engagement.title} />
      ) : <p className="card workspace-muted">Select an engagement in the header to open its discussion draft.</p>}
    </div>
  );
}

function DiscussionDraft({ storageKey, title }: { storageKey: string; title: string }) {
  const [draft, setDraft] = useState("");
  const [status, setStatus] = useState("");

  useEffect(() => {
    try {
      setDraft(localStorage.getItem(storageKey) ?? "");
    } catch {
      setStatus("Could not read the saved draft.");
    }
  }, [storageKey]);

  function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    try {
      localStorage.setItem(storageKey, draft);
      setStatus("Draft saved.");
    } catch {
      setStatus("Could not save the draft.");
    }
  }

  return (
    <form className="card" onSubmit={save}>
      <h2 className="mb-3">{title}</h2>
      <label htmlFor="discussion-draft" className="block mb-2">Discussion draft</label>
      <textarea id="discussion-draft" rows={6} maxLength={8000} value={draft} onChange={(e) => { setDraft(e.target.value); setStatus(""); }} />
      <div className="flex flex-wrap items-center gap-3 mt-3">
        <button className="btn-primary" type="submit">Save draft</button>
        {status && <p role="status">{status}</p>}
      </div>
    </form>
  );
}
