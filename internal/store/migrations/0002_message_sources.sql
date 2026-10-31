-- Web search sources attached to a message, stored as a JSON array of
-- {"title","url"} objects. Empty for messages generated without web search.
ALTER TABLE messages ADD COLUMN sources TEXT NOT NULL DEFAULT '';
