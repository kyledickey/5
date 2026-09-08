-- Synthetic rows in the actual v4 column format; no production identities.
INSERT INTO cases (id, user_id, moderator_id, guild_id, reason, type, created_at, context_url) VALUES
('legacy-warn', '1001', '2001', '3001', 'Warning: café 🦆', 0, '2024-01-02 03:04:05', NULL),
('legacy-ban', '1002', '2001', '3001', 'Ban: original reason', 1, '2024-01-02 03:04:05', 'https://discord.com/channels/3001/4001/5001'),
('legacy-kick', '1003', '2002', '3001', 'Kick: line one
line two', 2, '2024-01-03 03:04:05', NULL),
('legacy-unban', '1002', '2002', '3001', 'Unban: appeal accepted', 3, '2024-01-04 03:04:05', NULL),
('legacy-timeout', '1004', '2002', '3001', 'Timeout: original reason', 4, '2024-01-05 03:04:05', NULL),
('legacy-delete', '1005', '2002', '3001', 'Message deletion: original reason', 5, '2024-01-06 03:04:05', 'https://discord.com/channels/3001/4001/5002'),
('other-guild', '1001', '2001', '9999', 'Must not be exported', 99, '2024-01-07 03:04:05', NULL);
