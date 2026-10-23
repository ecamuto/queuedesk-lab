-- Eseguito da PostgreSQL solo alla prima inizializzazione del volume.
-- Il database di test è separato: gli integration test fanno TRUNCATE.
CREATE DATABASE queuedesk_test;
